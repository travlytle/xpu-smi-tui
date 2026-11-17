# fish completion for xpu-smi-tui

complete -c xpu-smi-tui -s d -l demo -d "Start in simulation/demo mode"
complete -c xpu-smi-tui -l demo-cards -d "Simulated cards in demo mode (implies --demo)" -x -a "1 2 3 4 6 8"
complete -c xpu-smi-tui -s i -l interval -d "Telemetry refresh interval in seconds" -x -a "1 2 3 5 10"
complete -c xpu-smi-tui -l device -d "GPU to focus at start (-1 shows all)" -x -a "-1 0 1 2 3 4 5 6 7"
complete -c xpu-smi-tui -l gpu-order -d "PCI addresses in numbering order, e.g. 03:00.0,04:00.0" -x
complete -c xpu-smi-tui -l idle-poll -d "Seconds between samples of a suspended card" -x -a "0 10 30 60 off"
complete -c xpu-smi-tui -l theme -d "Colour theme" -x -a "default dracula nord gruvbox catppuccin-mocha catppuccin-latte tokyo-night solarized-dark one-dark monokai rose-pine"
complete -c xpu-smi-tui -s v -l version -d "Print version and exit"
complete -c xpu-smi-tui -s h -l help -d "Print help message and exit"
