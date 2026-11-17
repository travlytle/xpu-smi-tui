# bash completion for xpu-smi-tui

_xpu_smi_tui_completions() {
    local cur prev opts
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    opts="-d --demo --demo-cards -i --interval --device --gpu-order --idle-poll --theme -v --version -h --help"

    case "${prev}" in
        -i|--interval)
            COMPREPLY=( $(compgen -W "1 2 3 5 10" -- "${cur}") )
            return 0
            ;;
        --device)
            COMPREPLY=( $(compgen -W "-1 0 1 2 3 4 5 6 7" -- "${cur}") )
            return 0
            ;;
        --demo-cards)
            COMPREPLY=( $(compgen -W "1 2 3 4 6 8" -- "${cur}") )
            return 0
            ;;
        --idle-poll)
            COMPREPLY=( $(compgen -W "0 10 30 60 off" -- "${cur}") )
            return 0
            ;;
        --theme)
            COMPREPLY=( $(compgen -W "default dracula nord gruvbox catppuccin-mocha catppuccin-latte tokyo-night solarized-dark one-dark monokai rose-pine" -- "${cur}") )
            return 0
            ;;
        --gpu-order)
            return 0
            ;;
    esac

    if [[ ${cur} == -* ]] ; then
        COMPREPLY=( $(compgen -W "${opts}" -- "${cur}") )
        return 0
    fi
}

complete -F _xpu_smi_tui_completions xpu-smi-tui
