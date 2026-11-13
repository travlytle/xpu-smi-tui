APP_NAME=xpu-smi-tui
EXPORTER_NAME=xpu-exporter
VERSION := $(shell cat VERSION)
LDFLAGS := -s -w -X main.AppVersion=$(VERSION)

.PHONY: all build build-exporter run demo test install uninstall deb clean

all: build build-exporter

build:
	go build -ldflags="$(LDFLAGS)" -o $(APP_NAME) .

# The always-on Prometheus exporter (pkg/exporter) -- a separate binary from
# the interactive TUI, so one can run as a systemd service without the other.
build-exporter:
	go build -ldflags="-s -w" -o $(EXPORTER_NAME) ./cmd/xpu-exporter

run: build
	./$(APP_NAME)

demo: build
	./$(APP_NAME) --demo

test:
	go test -v ./...

install: build
	@chmod +x install.sh
	./install.sh

uninstall:
	@chmod +x uninstall.sh
	./uninstall.sh

deb:
	@chmod +x scripts/build_deb.sh
	./scripts/build_deb.sh

clean:
	# build/ and *.deb are generated; nothing tracked lives there any more
	rm -rf $(APP_NAME) $(EXPORTER_NAME) build *.deb
