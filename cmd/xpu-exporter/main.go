// Command xpu-exporter is the always-on counterpart to xpu-smi-tui: it polls
// the same collector and serves the result in Prometheus's text format, so a
// Prometheus + Grafana stack elsewhere on the network gets continuous
// history without anyone needing a terminal open. See pkg/exporter for what
// it exports and, deliberately, what it leaves out.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/travlytle/xpu-smi-tui/pkg/exporter"
	"github.com/travlytle/xpu-smi-tui/pkg/xpu"
)

func main() {
	listen := flag.String("listen", ":9835", "address to serve /metrics on")
	interval := flag.Duration("interval", time.Second, "how often to poll the GPUs")
	idlePoll := flag.String("idle-poll", os.Getenv("XPU_IDLE_POLL"), "Seconds between samples of a runtime-suspended card, which is otherwise left alone because reading it wakes it and roughly doubles its draw: a number, 0 to never wake one, or off to always poll (default 30; also $XPU_IDLE_POLL)")
	gpuOrder := flag.String("gpu-order", os.Getenv("XPU_GPU_ORDER"), "PCI addresses in the order the `device` label numbers GPUs, e.g. 03:00.0,04:00.0 (default: PCI address order)")
	flag.Parse()

	idle, err := xpu.ParseIdlePolicy(*idlePoll)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	col := xpu.NewLiveCollector()
	col.SetIdlePolicy(idle)
	col.SetGPUOrder(xpu.ParseGPUOrder(*gpuOrder))
	defer col.Close()

	reg := prometheus.NewRegistry()
	exp := exporter.New(col, *interval, reg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go exp.Run(ctx)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "xpu-exporter\n\n<a href=\"/metrics\">/metrics</a>\n")
	})

	srv := &http.Server{Addr: *listen, Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("xpu-exporter listening on %s (poll every %s, Level Zero Sysman)", *listen, *interval)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
