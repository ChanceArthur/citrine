# shellcheck shell=bash
# A site's citrine.conf comes from its repository, so it's read as data and never
# sourced. A key here reaches root, so it needs a check in conf_fits. Anything else
# that isn't the machine's is the site's own, kept as SITE_KEY_<name> for its env
# templates and out of the way of every other variable.

conf_defaults=/usr/lib/citrine/defaults.conf
conf_secrets=/usr/lib/citrine/secrets.conf
conf_keys=" REGISTRY BUILD DATABASES BACKUP_PATHS BACKUP_DRIVE_PATHS BACKUP_EXCLUDE SERVE SERVE_TCP "

conf_key() { [[ $conf_keys == *" $1 "* ]]; }

conf_secret() { grep -q "^$1=" "$conf_secrets"; }

conf_reserved() {
  printf '%s|' SITE_REPO PATH IFS HOME SHELL ENV USER LOGNAME TMPDIR TZ LANG SHELLOPTS UID EUID PPID GROUPS GLOBIGNORE CDPATH POSIXLY_CORRECT 'PS[0-9]' 'BASH[A-Z_]*' \
    'LC_[A-Z_]+' 'LD_[A-Z_]+' 'XDG_[A-Z_]+' 'DBUS_[A-Z_]+' 'SYSTEMD_[A-Z_]+' 'RESTIC_[A-Z_]+' 'AWS_[A-Z_]+' \
    'GIT_[A-Z_]+' 'SITE_KEY_[A-Z0-9_]+'
  sed -n 's/^\([A-Z][A-Z0-9_]*\)=.*/\1/p' "$conf_defaults" | paste -sd'|' -
}

conf_fits() {
  local word='[a-z0-9][a-z0-9-]*' path='[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*' re
  [ -n "$2" ] || return 0
  case $1 in
    REGISTRY) re='^[a-z0-9][a-z0-9.-]*(:[0-9]+)?(/[a-z0-9][a-z0-9._-]*)*$' ;;
    DATABASES) re="^$word=$word( +$word=$word)*\$" ;;
    SERVE | SERVE_TCP) re='^[0-9]+=[0-9]+( +[0-9]+=[0-9]+)*$' ;;
    BUILD)
      re="^$word=(\\.|$path)( +$word=(\\.|$path))*\$"
      [[ $2 =~ $re ]] || return 1
      local pair
      for pair in $2; do
        pair=${pair#*=}
        [ "$pair" = . ] || [[ /$pair/ != *"/./"* && /$pair/ != *"/../"* ]] || return 1
      done
      ;;
    *)
      re="^$path( +$path)*\$"
      [[ " ${2//\// } " != *" .. "* && " ${2//\// } " != *" . "* ]] || return 1
      ;;
  esac
  [[ $2 =~ $re ]]
}

conf_problem() {
  if conf_key "$1"; then
    conf_fits "$1" "$2" && return 0
    case $1 in
      BUILD) echo "BUILD doesn't fit: name=folder pairs, each a plain word and a folder inside the repository" ;;
      *) echo "$1 doesn't fit: its paths stay inside the pool, and its names are plain words" ;;
    esac
    return 0
  fi
  if grep -q "^$1=" "$conf_defaults" "$conf_secrets" 2>/dev/null; then
    echo "$1 is the machine's to set, not the site's"
  fi
}

conf_walk() {
  local n=0 line k v
  while IFS= read -r line || [ -n "$line" ]; do
    n=$((n + 1))
    line=${line%$'\r'}
    line=${line#"${line%%[![:space:]]*}"}
    line=${line%"${line##*[![:space:]]}"}
    case $line in '' | '#'*) continue ;; esac
    if [[ ! $line =~ ^[A-Z][A-Z0-9_]*= ]]; then
      "$2" "$n" "" ""
      continue
    fi
    k=${line%%=*}
    v=${line#*=}
    case $v in
      \'*\' | \"*\")
        v=${v#?}
        v=${v%?}
        ;;
    esac
    "$2" "$n" "$k" "$v"
  done <"$1"
}

conf_take() {
  [ -n "$2" ] || return 0
  [ -z "$(conf_problem "$2" "$3")" ] || return 0
  if conf_key "$2"; then
    printf -v "$2" '%s' "$3"
  else
    printf -v "SITE_KEY_$2" '%s' "$3"
  fi
}

conf_read() {
  local v
  for v in ${!SITE_KEY_@}; do unset "$v"; done
  [ -r "$1" ] || return 0
  conf_walk "$1" conf_take
}

conf_report() {
  local p
  if [ -z "$2" ]; then
    echo "citrine.conf line $1 isn't KEY=value"
    return 0
  fi
  p=$(conf_problem "$2" "$3")
  [ -z "$p" ] || echo "citrine.conf line $1: $p"
}

conf_check() {
  local out
  out=$(conf_walk "$1" conf_report)
  [ -z "$out" ] || {
    printf '%s\n' "$out"
    return 1
  }
}
