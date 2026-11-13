/*
 * pcie-copy-bench: move an exactly known number of bytes across one GPU's PCIe
 * link with Level Zero, so PCIe counters can be checked against the truth.
 *
 *   pcie-copy-bench <pci-bdf> [size-MiB=256] [phase-seconds=5]
 *
 * Phases: 2 s idle, <phase> s host->device, 2 s idle, <phase> s device->host,
 * 2 s idle. Prints one machine-readable line per phase:
 *   PHASE <name> <start-epoch> <end-epoch> <MiB-moved>
 *
 * Build (needs ze_api.h from github.com/oneapi-src/level-zero, matching the
 * installed loader):
 *   gcc -O2 -I<dir containing level_zero/> -o pcie-copy-bench pcie-copy-bench.c \
 *       /usr/lib/x86_64-linux-gnu/libze_loader.so.1
 */
#include <level_zero/ze_api.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <time.h>
#include <unistd.h>

#define CK(x) do { ze_result_t r_ = (x); if (r_) { fprintf(stderr, "%s failed: 0x%x\n", #x, r_); exit(1); } } while (0)

static double now(void) {
	struct timespec t;
	clock_gettime(CLOCK_REALTIME, &t);
	return t.tv_sec + t.tv_nsec / 1e9;
}

static ze_device_handle_t find_device(const char *bdf, ze_driver_handle_t *drv_out) {
	uint32_t nd = 0;
	CK(zeDriverGet(&nd, NULL));
	ze_driver_handle_t *drivers = calloc(nd, sizeof *drivers);
	CK(zeDriverGet(&nd, drivers));
	for (uint32_t i = 0; i < nd; i++) {
		uint32_t n = 0;
		CK(zeDeviceGet(drivers[i], &n, NULL));
		ze_device_handle_t *devs = calloc(n, sizeof *devs);
		CK(zeDeviceGet(drivers[i], &n, devs));
		for (uint32_t j = 0; j < n; j++) {
			ze_pci_ext_properties_t pci = {.stype = ZE_STRUCTURE_TYPE_PCI_EXT_PROPERTIES};
			if (zeDevicePciGetPropertiesExt(devs[j], &pci) != ZE_RESULT_SUCCESS) continue;
			char got[32];
			snprintf(got, sizeof got, "%04x:%02x:%02x.%x", pci.address.domain, pci.address.bus,
			         pci.address.device, pci.address.function);
			if (strcasecmp(got, bdf) == 0) { *drv_out = drivers[i]; return devs[j]; }
		}
	}
	fprintf(stderr, "no Level Zero device at %s\n", bdf);
	exit(1);
}

int main(int argc, char **argv) {
	if (argc < 2) { fprintf(stderr, "usage: %s <pci-bdf> [MiB] [phase-s]\n", argv[0]); return 2; }
	size_t mib = argc > 2 ? strtoul(argv[2], 0, 10) : 256;
	int phase = argc > 3 ? atoi(argv[3]) : 5;
	size_t size = mib << 20;

	CK(zeInit(0));
	ze_driver_handle_t drv;
	ze_device_handle_t dev = find_device(argv[1], &drv);

	ze_context_desc_t cd = {.stype = ZE_STRUCTURE_TYPE_CONTEXT_DESC};
	ze_context_handle_t ctx;
	CK(zeContextCreate(drv, &cd, &ctx));

	ze_device_mem_alloc_desc_t dd = {.stype = ZE_STRUCTURE_TYPE_DEVICE_MEM_ALLOC_DESC};
	ze_host_mem_alloc_desc_t hd = {.stype = ZE_STRUCTURE_TYPE_HOST_MEM_ALLOC_DESC};
	void *dbuf, *hbuf;
	CK(zeMemAllocDevice(ctx, &dd, size, 4096, dev, &dbuf));
	CK(zeMemAllocHost(ctx, &hd, size, 4096, &hbuf));
	memset(hbuf, 0xA5, size); /* touch it: pinned pages must be resident */

	ze_command_queue_desc_t qd = {.stype = ZE_STRUCTURE_TYPE_COMMAND_QUEUE_DESC,
	                              .mode = ZE_COMMAND_QUEUE_MODE_SYNCHRONOUS};
	ze_command_list_handle_t cl;
	CK(zeCommandListCreateImmediate(ctx, dev, &qd, &cl));

	/* warm-up, outside every phase */
	CK(zeCommandListAppendMemoryCopy(cl, dbuf, hbuf, size, NULL, 0, NULL));
	CK(zeCommandListAppendMemoryCopy(cl, hbuf, dbuf, size, NULL, 0, NULL));

	const char *names[] = {"idle0", "h2d", "idle1", "d2h", "idle2"};
	for (int p = 0; p < 5; p++) {
		int copying = (p == 1 || p == 3);
		double start = now(), end = start + (copying ? phase : 2);
		unsigned long long moved = 0;
		if (!copying) sleep(2);
		while (copying && now() < end) {
			void *dst = p == 1 ? dbuf : hbuf, *src = p == 1 ? hbuf : dbuf;
			CK(zeCommandListAppendMemoryCopy(cl, dst, src, size, NULL, 0, NULL));
			moved += size;
		}
		printf("PHASE %s %.3f %.3f %llu\n", names[p], start, now(), moved >> 20);
		fflush(stdout);
	}
	return 0;
}
