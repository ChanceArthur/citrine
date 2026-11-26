#!/bin/bash
set -u
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fails=0

mkdir -p "$T/bin" "$T/pool"
for c in mount chattr; do printf '#!/bin/bash\necho "%s $*" >>"%s/calls"\n' "$c" "$T" >"$T/bin/$c"; done
cat >"$T/bin/mkfs.btrfs" <<EOF
#!/bin/bash
echo "mkfs \$*" >>"$T/calls"
EOF
cat >"$T/bin/mountpoint" <<EOF
#!/bin/bash
[ -e "$T/mounted" ]
EOF
cat >"$T/bin/blkid" <<EOF
#!/bin/bash
[ -e "$T/has-label" ] && echo /dev/fake
EOF
cat >"$T/bin/btrfs" <<EOF
#!/bin/bash
case "\$1 \${2:-}" in
  "device ready") [ -e "$T/has-label" ] && [ ! -e "$T/not-ready" ] ;;
  *) exit 0 ;;
esac
EOF
chmod +x "$T"/bin/*

cat >"$T/common" <<EOF
set -euo pipefail
POOL_MOUNT=$T/pool; POOL_LABEL=test-data; POOL_SEED_PART=$T/seed; POOL_WAIT_SEC=0
log() { printf '%s\n' "\$*"; }
die() { printf '%s\n' "\$*" >&2; exit 1; }
pool_dirs() { :; }
EOF
sed -e "s|/usr/libexec/citrine/common|$T/common|" \
  -e 's|\[ -b "\$POOL_SEED_PART" \]|[ -e "$POOL_SEED_PART" ]|' \
  "$repo/tree/usr/libexec/citrine/pool" >"$T/pool.sh"

run() {
  : >"$T/calls"
  PATH="$T/bin:$PATH" bash "$T/pool.sh" >"$T/out" 2>&1
  rc=$?
}

want() {
  if eval "$2"; then
    echo "ok    $1"
  else
    echo "FAIL  $1 (rc=$rc)"
    cat "$T/out" "$T/calls" | sed 's/^/      /'
    fails=$((fails + 1))
  fi
}

reset() { rm -f "$T/has-label" "$T/seed" "$T/mounted" "$T/not-ready" "$T/pool/.pool-ready"; }

reset
touch "$T/has-label" "$T/seed"
run
want "a labelled pool is adopted, even with a seed partition" '[ $rc = 0 ] && ! grep -q mkfs "$T/calls" && grep -q "mount.*LABEL=test-data" "$T/calls"'

reset
touch "$T/has-label" "$T/seed" "$T/not-ready"
run
want "a labelled pool that isn't ready is still never formatted" '! grep -q mkfs "$T/calls"'

reset
run
want "no pool and no seed partition stops, and formats nothing" '[ $rc != 0 ] && ! grep -q mkfs "$T/calls" && [ ! -e "$T/pool/.pool-ready" ]'

reset
touch "$T/seed"
run
want "no pool anywhere makes one on the seed partition, once" '[ $rc = 0 ] && [ "$(grep -c mkfs "$T/calls")" = 1 ] && grep -q "mkfs.*-L test-data.*$T/seed" "$T/calls"'

reset
touch "$T/mounted" "$T/seed"
run
want "an already mounted pool is left alone" '[ $rc = 0 ] && ! grep -q -E "mkfs|mount " "$T/calls"'

[ "$fails" = 0 ] && echo "pool: all passed" || { echo "pool: $fails failed"; exit 1; }
