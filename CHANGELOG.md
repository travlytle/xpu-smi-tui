# Changelog

## 0.2.3

- Trends adds Compute, Clock and Memory bandwidth, and orders every layout the same way: activity (compute, clock), power and cooling, memory (VRAM use, bandwidth), then temperatures. All charts are shown in every layout; the view scrolls when they do not fit.
- Lines and stripes size their charts to the legend, so 6-8 cards fit without scrolling at 140x58.
- Demo clocks follow each card's load. README GIF updated.

## 0.2.2

- The .deb now includes `xpu-exporter` and its systemd unit (it shipped only the TUI), plus the Grafana dashboard and this changelog; upgrades restart running exporters. `install.sh` installs the exporter too.

## 0.2.1

- Trends: three styles, cycled with `v` and saved: stripes (alternating colours where cards overlap), lines, and per card (a small graph per card shaded by value, btop style; `s` switches braille / block). The default follows the card count: stripes for 2, lines for 3, per card from 4. Every theme has 8 card colours.
- Detail: memory type and bus width, Resizable BAR (warns when off), option ROM version, ECC "not supported" where absent, and a health row when the driver says a reset is needed.
- Power: the cap now shows on cards with only a package limit (Arc A40 on `xe`); its averaging window is in the right unit (milliseconds, previously printed as seconds); "critical" is now "spike limit" and hidden when absent.
- Exporter: `xpu_gpu_bar_bytes` and `xpu_gpu_reset_required`; the Grafana dashboard adds a Card health table.
- Demo: six cards with distinct loads by default; `--demo-cards N` (1-16).
- Help, legend and shell completions cover the new keys and flags.

## 0.2.0

- Themes: default, dracula, nord, gruvbox, catppuccin-mocha, catppuccin-latte, tokyo-night, solarized-dark, one-dark, monokai, rose-pine. `--theme` / `$XPU_THEME`, or `t` to cycle; the choice is saved.
- Trend charts span the full 60 s window at any width.

## 0.1.0

- First release.
