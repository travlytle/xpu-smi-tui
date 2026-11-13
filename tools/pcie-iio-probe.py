#!/usr/bin/env python3
"""Prototype: measure each GPU's PCIe traffic from the CPU's IIO counters and
compare it with what xpu-smi reports, to find out whether the counters can be
trusted (B70s) and whether they can fill the gap (A40, which xpu-smi gives none).

Needs root or CAP_PERFMON (perf_event_paranoid is 2):

    sudo tools/pcie-iio-probe.py            # drives its own load, ~1 min
    sudo tools/pcie-iio-probe.py --passive 30   # just watch for 30 s, no load
    sudo tools/pcie-iio-probe.py --raw         # also print the per-second table

By default it runs tools/pcie-copy-bench on each GPU in turn (256 MiB buffers, a
few GB moved each way, no models touched) and compares every phase against the
exact number of bytes the bench moved.

Skylake-X only: the uncore_iio_free_running_N PMUs count bytes crossing each
PCIe root stack. "in" is device -> host, "out" is host -> device (to be
confirmed against xpu-smi by this run). It reads xpu-smi's numbers from the
xpu-exporter, which must be running on :9835, rather than starting a second
`xpu-smi dump` that would fight the exporter for the one OA stream.
"""
import os, re, subprocess, sys, threading, time, urllib.request
from collections import defaultdict

ARGS = sys.argv[1:]
RAW = "--raw" in ARGS
PASSIVE = "--passive" in ARGS
NUMS = [a for a in ARGS if a.isdigit()]
BENCH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "pcie-copy-bench")
PHASE_S = 5
EXPORTER = os.environ.get("XPU_EXPORTER", "http://127.0.0.1:9835/metrics")
PORTS = range(4)
MIB = 1024 * 1024


def gpu_stacks():
    """Map each Intel GPU's BDF to the IIO stack (uncore_iio_N) it sits behind,
    using the root bus each stack reports in sysfs."""
    root_of = {}
    for n in range(6):
        try:
            with open(f"/sys/devices/uncore_iio_{n}/die0") as f:
                root_of[f.read().strip().split(":")[-1].lower()] = n
        except OSError:
            pass
    out = {}
    for dev in sorted(os.listdir("/sys/bus/pci/devices")):
        try:
            if open(f"/sys/bus/pci/devices/{dev}/vendor").read().strip() != "0x8086":
                continue
            if not open(f"/sys/bus/pci/devices/{dev}/class").read().startswith("0x03"):
                continue
        except OSError:
            continue
        m = re.search(r"/pci[0-9a-f]{4}:([0-9a-f]{2})/", os.path.realpath(f"/sys/bus/pci/devices/{dev}") + "/")
        if m and m.group(1) in root_of:
            out[dev] = root_of[m.group(1)]
    return out


def parse_perf_line(line):
    """`time,value,unit,event,...` from `perf stat -x, -I`; None if not a sample."""
    f = line.strip().split(",")
    if len(f) < 4 or not re.match(r"^\d+\.\d+$", f[0]):
        return None
    try:
        return float(f[0]), float(f[1]), f[3]
    except ValueError:
        return None  # "<not counted>" / "<not supported>"


def exporter_pcie():
    """{device: (rx_MiB_s, tx_MiB_s), ...} plus {device: bdf} from the exporter."""
    text = urllib.request.urlopen(EXPORTER, timeout=2).read().decode()
    pcie, bdf = defaultdict(dict), {}
    for line in text.splitlines():
        m = re.match(r'xpu_gpu_pcie_bytes_per_second\{device="(\d+)",direction="(rx|tx)"\} (\S+)', line)
        if m:
            pcie[m.group(1)][m.group(2)] = float(m.group(3)) / MIB
        m = re.match(r'xpu_gpu_info\{.*device="(\d+)".*pci_bdf="([^"]+)"', line)
        if m:
            bdf[m.group(1)] = m.group(2)
    return pcie, bdf


def run_bench(bdf, log):
    """Run the copy bench on one GPU; append (bdf, phase, start, end, MiB) to log."""
    p = subprocess.run([BENCH, bdf, "256", str(PHASE_S)], capture_output=True, text=True)
    for line in p.stdout.splitlines():
        f = line.split()
        if len(f) == 5 and f[0] == "PHASE":
            log.append((bdf, f[1], float(f[2]), float(f[3]), float(f[4])))
    if p.returncode != 0:
        print(f"bench on {bdf} failed: {p.stderr.strip()}", file=sys.stderr)


def main():
    stacks = gpu_stacks()
    if not stacks:
        sys.exit("no Intel GPU behind a uncore_iio stack found")
    print("GPU -> IIO stack:", {b: f"iio_{n}" for b, n in stacks.items()})
    load = not PASSIVE
    if load and not os.access(BENCH, os.X_OK):
        sys.exit(f"{BENCH} not built -- see the build line in tools/pcie-copy-bench.c, or use --passive")

    per_gpu_s = 2 + 2 * PHASE_S + 6 + 3  # three idle gaps, two copy phases, startup
    duration = int(NUMS[0]) if NUMS else (4 + len(stacks) * per_gpu_s + 3 if load else 30)

    events = [f"uncore_iio_free_running_{n}/bw_{d}_port{p}/"
              for n in sorted(set(stacks.values())) for d in ("in", "out") for p in PORTS]
    cmd = ["perf", "stat", "-a", "-x,", "-I", "1000", "-e", ",".join(events), "--", "sleep", str(duration)]
    proc = subprocess.Popen(cmd, stderr=subprocess.PIPE, text=True)

    xs = []  # (epoch, {device: (rx, tx)})
    stop = threading.Event()
    bdf_of_device = {}

    def poll():
        while not stop.is_set():
            try:
                pcie, bdf = exporter_pcie()
                bdf_of_device.update(bdf)
                xs.append((time.time(), {d: (v.get("rx"), v.get("tx")) for d, v in pcie.items()}))
            except Exception:  # exporter down: keep sampling IIO anyway
                xs.append((time.time(), {}))
            stop.wait(1)
    threading.Thread(target=poll, daemon=True).start()

    bench_log = []
    def drive():
        time.sleep(4)
        for b in stacks:
            print(f"  load on {b} ...", flush=True)
            run_bench(b, bench_log)
    if load:
        threading.Thread(target=drive, daemon=True).start()

    # per[epoch-of-line] = {(stack, dir, port): MiB in the 1 s interval ending then}
    per = defaultdict(lambda: defaultdict(float))
    rows = []
    for line in proc.stderr:
        s_ = parse_perf_line(line)
        if not s_:
            if line.strip():
                print("perf:", line.strip(), file=sys.stderr)
            continue
        t, mib, name = s_
        m = re.match(r"uncore_iio_free_running_(\d+)/bw_(in|out)_port(\d)/", name)
        if m:
            per[t][(int(m.group(1)), m.group(2), int(m.group(3)))] += mib
            if not rows or rows[-1][0] != t:
                rows.append((t, time.time()))  # perf time, arrival epoch (~ interval end)
    proc.wait()
    stop.set()

    if not per:
        sys.exit("no perf samples -- run as root (sudo)")

    dev_of_bdf = {b: d for d, b in bdf_of_device.items()}
    end_epoch = {t: e for t, e in rows}

    def iio(bdf, direction, t0, t1):
        n = stacks[bdf]
        return sum(v for t, d in per.items() if t0 <= end_epoch[t] - 1 and end_epoch[t] <= t1
                   for (s_, dr, p), v in d.items() if s_ == n and dr == direction)

    def xpu(bdf, idx, t0, t1):
        dev = dev_of_bdf.get(bdf, "")
        vals = [v[dev][idx] for e, v in xs if t0 <= e <= t1 and dev in v and v[dev][idx] is not None]
        return sum(vals) if vals else None  # 1 sample per second -> MiB

    if RAW:
        print(f"\n{'t':>4}  " + "  ".join(f"{b.split(':',1)[1]:>9} in/out MiB/s" for b in stacks))
        for i, t in enumerate(sorted(per)):
            cells = []
            for b, n in stacks.items():
                i_ = sum(v for (s_, d, p), v in per[t].items() if s_ == n and d == "in")
                o_ = sum(v for (s_, d, p), v in per[t].items() if s_ == n and d == "out")
                cells.append(f"{i_:8.2f} {o_:8.2f}")
            print(f"{i+1:>4}  " + "    ".join(cells))

    if bench_log:
        print("\nCOPY BENCH vs COUNTERS (MiB over each phase, +/-1 s margin; truth = bytes the bench moved)")
        print(f"{'gpu':<13}{'phase':<6}{'truth':>8}  {'IIO in':>8} {'IIO out':>8}  {'xpu rx':>8} {'xpu tx':>8}   IIO/truth (in, out)   xpu/truth (rx, tx)")
        for bdf, phase, t0, t1, truth in bench_log:
            if phase not in ("h2d", "d2h"):
                continue
            t0, t1 = t0 - 1, t1 + 1
            ii, oo = iio(bdf, "in", t0, t1), iio(bdf, "out", t0, t1)
            rx, tx = xpu(bdf, 0, t0, t1), xpu(bdf, 1, t0, t1)
            r = lambda a, c: "   n/a" if a is None or c <= 0 else f"{a / c:6.2f}"
            f = lambda v: "     n/a" if v is None else f"{v:8.1f}"
            print(f"{bdf:<13}{phase:<6}{truth:8.0f}  {ii:8.1f} {oo:8.1f}  {f(rx)} {f(tx)}   {r(ii, truth)} {r(oo, truth)}          {r(rx, truth)} {r(tx, truth)}")
        print("\nReading it: the direction that carries the truth (~1.00) is the counter for that direction;")
        print("the other should be near 0. xpu/truth near 1.00 means xpu-smi's PCIe figure is right.")

    busy_ports = sorted({(n, p) for t in per for (n, d, p), v in per[t].items() if v > 0.5})
    print("\nports that carried real traffic (stack, port):", busy_ports)


if __name__ == "__main__":
    main()
