# fish completion for xpu-smi-tui

complete -c xpu-smi-tui -s d -l demo -d "Start in simulation/demo mode"
complete -c xpu-smi-tui -s i -l interval -d "Telemetry refresh interval in seconds" -x -a "1 2 3 5 10"
complete -c xpu-smi-tui -l device -d "Initial GPU device index to focus" -x -a "0 1 2 3"
complete -c xpu-smi-tui -s v -l version -d "Print version and exit"
complete -c xpu-smi-tui -s h -l help -d "Print help message and exit"
