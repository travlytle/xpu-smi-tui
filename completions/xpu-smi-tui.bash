# bash completion for xpu-smi-tui

_xpu_smi_tui_completions() {
    local cur prev opts
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    opts="-d --demo -i --interval --device -v --version -h --help"

    case "${prev}" in
        -i|--interval)
            COMPREPLY=( $(compgen -W "1 2 3 5 10" -- "${cur}") )
            return 0
            ;;
        --device)
            COMPREPLY=( $(compgen -W "0 1 2 3" -- "${cur}") )
            return 0
            ;;
        *)
            ;;
    esac

    if [[ ${cur} == -* ]] ; then
        COMPREPLY=( $(compgen -W "${opts}" -- "${cur}") )
        return 0
    fi
}

complete -F _xpu_smi_tui_completions xpu-smi-tui
