case $- in
  *i*)
    if [ -z "${CITRINE_DASHBOARD:-}" ] && [ -t 0 ] && [ -t 1 ] && shopt -q login_shell 2>/dev/null; then
      dashboard --login && exit
    fi
    ;;
esac
