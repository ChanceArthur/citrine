#!/bin/bash
set -u
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
L=$repo/tree/usr/lib/citrine
X=$repo/tree/usr/libexec/citrine
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fails=0
rc=0
: >"$T/out"
: >"$T/calls"

mkdir -p "$T/lib" "$T/etc" "$T/bin" "$T/pool"
sed -e "s|^POOL_MOUNT=.*|POOL_MOUNT=$T/pool|" "$L/defaults.conf" >"$T/lib/defaults.conf"
cp "$L/secrets.conf" "$T/lib/"
sed -e "s|/usr/lib/citrine/|$T/lib/|g" "$L/conf.sh" >"$T/lib/conf.sh"

want() {
  if eval "$2"; then
    echo "ok    $1"
  else
    echo "FAIL  $1 (rc=$rc)"
    sed 's/^/      /' "$T/out" "$T/calls"
    fails=$((fails + 1))
  fi
}

# Prints a variable after reading a conf, the way a root script would see it.
read_as() {
  printf '%s\n' "$1" >"$T/conf"
  (
    set -euo pipefail
    . "$T/lib/defaults.conf"
    . "$T/lib/conf.sh"
    conf_read "$T/conf"
    printf '%s' "${!2:-}"
  ) >"$T/out" 2>&1
  rc=$?
}
check() {
  printf '%s\n' "$1" >"$T/conf"
  (. "$T/lib/conf.sh" && conf_check "$T/conf") >"$T/out" 2>&1
  rc=$?
}
refused() {
  check "$1"
  want "refuses $2" '[ $rc != 0 ] && grep -q "line 1" "$T/out"'
}

read_as 'REGISTRY=ghcr.io/me' REGISTRY
want "takes a plain value" '[ $rc = 0 ] && [ "$(cat "$T/out")" = ghcr.io/me ]'
read_as 'BACKUP_PATHS="a/b c/d"' BACKUP_PATHS
want "strips a pair of double quotes" '[ "$(cat "$T/out")" = "a/b c/d" ]'
read_as "BACKUP_PATHS='a/b'" BACKUP_PATHS
want "strips a pair of single quotes" '[ "$(cat "$T/out")" = a/b ]'
read_as '  # a comment
REGISTRY=ghcr.io/a

' REGISTRY
want "skips comments and blank lines" '[ "$(cat "$T/out")" = ghcr.io/a ]'
printf 'SERVE=443=2283' >"$T/conf"
(. "$T/lib/defaults.conf"; . "$T/lib/conf.sh"; conf_read "$T/conf"; printf '%s' "$SERVE") >"$T/out" 2>&1
want "takes a last line with no newline" '[ "$(cat "$T/out")" = 443=2283 ]'
(set -euo pipefail; . "$T/lib/conf.sh"; conf_read "$T/nothing-here") >"$T/out" 2>&1
rc=$?
want "a missing conf is nothing, under set -eu" '[ $rc = 0 ]'

for v in '$(touch '"$T"'/pwned)' '`touch '"$T"'/pwned`' '"; touch '"$T"'/pwned; "' '$(touch '"$T"'/pwned)x y'; do
  read_as "MY_KEY=$v" SITE_KEY_MY_KEY
  want "a value never runs: $v" '[ ! -e "$T/pwned" ] && [ -s "$T/out" ]'
  read_as "REGISTRY=$v" REGISTRY
  want "and a Citrine key refuses it: $v" '[ ! -e "$T/pwned" ] && [ ! -s "$T/out" ]'
done
printf 'touch %s/pwned\nsource %s/evil\n. %s/evil\n' "$T" "$T" "$T" >"$T/conf"
echo "touch $T/pwned" >"$T/evil"
(set -e; . "$T/lib/conf.sh"; conf_read "$T/conf") >"$T/out" 2>&1
want "a line of shell never runs" '[ ! -e "$T/pwned" ]'
check 'touch /tmp/x'
want "and is reported" '[ $rc != 0 ] && grep -q "line 1" "$T/out" && grep -q "KEY=value" "$T/out"'

for k in IMAGE BACKUP_KEEP POOL_ADOPT_POLICY POOL_MOUNT USER_NAME USER_UID SITE_REF SITE_DIR TS_TAGS TIMEZONE HOSTNAME BACKUP_MOUNT \
  RESTIC_PASSWORD R2_BUCKET R2_ACCOUNT_ID GH_TOKEN TUNNEL_TOKEN; do
  read_as "$k=attacker" "$k"
  want "the repository can't set $k" '[ "$(cat "$T/out")" != attacker ]'
  refused "$k=attacker" "$k, and says so"
done
for k in PATH IFS LD_PRELOAD BASH_ENV ENV SHELLOPTS PS4; do
  read_as "$k=$T/evil" "$k"
  want "$k stays out of the shell" '[ "$(cat "$T/out")" != "$T/evil" ]'
  read_as "$k=$T/evil" "SITE_KEY_$k"
  want "kept only as the site's own key" '[ "$(cat "$T/out")" = "$T/evil" ]'
done
read_as 'conf_keys=x' conf_keys
want "a lowercase key sets nothing" '[ "$(cat "$T/out")" != x ]'
read_as 'SITE_KEY_X=1' SITE_KEY_X
want "SITE_KEY_ itself has no way past the prefix" '[ ! -s "$T/out" ]'

for line in 'BACKUP_PATHS=../../etc/sudoers.d' 'BACKUP_PATHS=a/../../etc' 'BACKUP_PATHS=/etc' 'BACKUP_PATHS=a ../b' 'BACKUP_PATHS=.' 'BACKUP_PATHS=*' 'BACKUP_PATHS=a/*' \
  'BACKUP_PATHS=/etc/shadow' 'BACKUP_PATHS=../../etc' 'BACKUP_DRIVE_PATHS=/root' 'BACKUP_EXCLUDE=$HOME' \
  'DATABASES=a' 'DATABASES=a=--privileged' 'DATABASES=../a=b' \
  'SERVE=x=1' 'SERVE=443=2283;id' 'SERVE_TCP=-1=2' 'REGISTRY=--x' 'REGISTRY=a b' 'REGISTRY=$(id)' \
  'BUILD=web' 'BUILD=web=..' 'BUILD=web=a/../../etc' 'BUILD=web=/etc' 'BUILD=web=./a' 'BUILD=web=a/.' 'BUILD=Web=a' \
  'BUILD=web=*' 'BUILD=web=a;id' 'BUILD=--x=a' 'BUILD=web=$(id)'; do
  k=${line%%=*}
  refused "$line" "$line"
  read_as "$line" "$k"
  want "and leaves it unset: $line" '[ ! -s "$T/out" ]'
done

read_as 'BUILD="web=. api=services/api"' BUILD
want "takes BUILD's pairs, the repository's root among them" '[ "$(cat "$T/out")" = "web=. api=services/api" ]'

check 'REGISTRY=ghcr.io/me
BUILD=web=app
SITE_URL=https://example.com
DATABASES="immich=immich-postgres cms=cms-postgres"
BACKUP_PATHS=cms/media
BACKUP_DRIVE_PATHS=cms/media immich/library
BACKUP_EXCLUDE=immich/library/thumbs immich/library/encoded-video
SERVE="443=2283 8443=8080"
SERVE_TCP=5433=5433
POSTGRES_USER=postgres
A_SITE_VALUE=anything at all $(here)'
want "a real conf is clean" '[ $rc = 0 ] && [ ! -s "$T/out" ]'

printf 'MY_KEY=old\n' >"$T/conf"
printf 'OTHER=new\n' >"$T/conf2"
(. "$T/lib/conf.sh"; conf_read "$T/conf"; conf_read "$T/conf2"; printf '%s|%s' "${SITE_KEY_MY_KEY:-gone}" "$SITE_KEY_OTHER") >"$T/out" 2>&1
want "a key the new conf dropped is forgotten" '[ "$(cat "$T/out")" = "gone|new" ]'

cat >"$T/bin/restic" <<EOF
#!/bin/bash
echo "restic \$*" >>"$T/calls"
exit 0
EOF
cat >"$T/bin/id" <<'EOF'
#!/bin/bash
echo 0
EOF
chmod +x "$T/bin"/*
sed -e "s|/usr/lib/citrine/|$T/lib/|g" -e "s|/etc/citrine/|$T/etc/|g" -e "s|^user_quadlets=.*|user_quadlets=$T/quadlets|" -e "s|^site_units=.*|site_units=$T/site-units|" "$X/common" >"$T/common"
sed -e "s|/usr/libexec/citrine/common|$T/common|" -e "s|/etc/citrine/|$T/etc/|g" "$X/backup" >"$T/backup"
cat >"$T/etc/citrine.conf" <<'EOF'
BACKUP_PATHS=cms/media
BACKUP_EXCLUDE=immich/library/thumbs
EOF
cat >"$T/etc/secrets.env" <<'EOF'
R2_ACCOUNT_ID='a'
R2_BUCKET='b'
R2_ACCESS_KEY_ID='c'
R2_SECRET_ACCESS_KEY='d'
RESTIC_PASSWORD='e'
EOF
PATH="$T/bin:$PATH" bash "$T/backup" r2 >"$T/out" 2>&1
rc=$?
want "a path that isn't there yet is skipped, and the rest still pruned" '[ $rc = 0 ] && ! grep -q "$T/pool/cms/media" "$T/calls" && grep -q "forget .*--prune" "$T/calls"'
mkdir -p "$T/pool/cms/media"
: >"$T/calls"
PATH="$T/bin:$PATH" bash "$T/backup" r2 >"$T/out" 2>&1
want "what's kept is counted per tag, so a changed set of paths doesn't start over" 'grep -q "forget .*--group-by host,tags" "$T/calls"'
want "backup reaches into the pool for a relative path" 'grep -q "backup --tag files --exclude=$T/pool/immich/library/thumbs .*$T/pool/cms/media" "$T/calls"'
echo 'BACKUP_KEEP="--keep-daily 1"' >"$T/etc/site.conf"
: >"$T/calls"
echo 'BACKUP_KEEP=--password-command=evil' >>"$T/etc/citrine.conf"
PATH="$T/bin:$PATH" bash "$T/backup" r2 >"$T/out" 2>&1
want "the machine sets BACKUP_KEEP, and the repository can't" 'grep -q "forget --keep-daily 1 --prune" "$T/calls" && ! grep -q evil "$T/calls"'

printf '#!/bin/bash\necho "chattr $*" >>"%s/calls"\n' "$T" >"$T/bin/chattr"
printf '#!/bin/bash\necho "install $*" >>"%s/calls"\nargs=()\nwhile [ $# -gt 0 ]; do case $1 in -o | -g) shift ;; *) args+=("$1") ;; esac; shift; done\nexec /usr/bin/install "${args[@]}"\n' "$T" >"$T/bin/install"
printf '#!/bin/bash\nshift 3\necho "as user: $*" >>"%s/calls"\nexec "$@"\n' "$T" >"$T/bin/runuser"
chmod +x "$T/bin"/*
mkdir -p "$T/quadlets"
printf 'ContainerName=db\nVolume=%s/app/postgres:/var/lib/postgresql:Z\nVolume=/etc/localtime:/etc/localtime:ro\n' "$T/pool" >"$T/quadlets/app-db.container"
printf 'Volume=%s/app/media:/media:Z\nVolume=%s/../escape:/x:Z\nVolume=%s/app/media/../../up:/y\nVolume=cache.volume:/cache\n' "$T/pool" "$T/pool" "$T/pool" >"$T/quadlets/app.container"
printf 'DATABASES=app=db\n' >"$T/etc/citrine.conf"
: >"$T/calls"
(PATH="$T/bin:$PATH"; . "$T/common"; pool_dirs) >"$T/out" 2>&1
rc=$?
want "the units' pool volumes are made" '[ $rc = 0 ] && [ -d "$T/pool/app/postgres" ] && [ -d "$T/pool/app/media" ]'
want "a database's, with copy-on-write off" 'grep -qx "chattr +C $T/pool/app/postgres" "$T/calls" && [ "$(grep -c "^chattr" "$T/calls")" = 1 ] && grep -q "as user: .*chattr +C $T/pool/app/postgres" "$T/calls"'
want "root makes only a volume's top folder, and the user the rest" '[ -z "$(grep "^install" "$T/calls" | grep -v -e "install -d -o 1001 -g 1001 $T/pool/app$")" ] && grep -q "as user: .*mkdir -p $T/pool/app/media" "$T/calls"'
printf 'Volume=%s/citrine-setup/x:/x:Z\nVolume=%s/.hidden/x:/y:Z\n' "$T/pool" "$T/pool" >"$T/quadlets/grab.container"
(PATH="$T/bin:$PATH"; . "$T/common"; pool_dirs) >"$T/out2" 2>&1
rm "$T/quadlets/grab.container"
want "no unit makes one of Citrine's own pool folders, or a hidden one" '[ ! -e "$T/pool/citrine-setup" ] && [ ! -e "$T/pool/.hidden" ] && grep -q "couldn.t make citrine-setup/x" "$T/out2"'
want "a volume leaving the pool isn't" '[ ! -e "$T/escape" ] && [ ! -e "$T/pool/up" ] && [ ! -e "$T/up" ] && [ "$(grep -c "leaves the pool" "$T/out")" = 2 ]'

printf 'Wants=app-db.service\n' >>"$T/quadlets/app.container"
printf 'Requires=citrine-data.service app.service\nVolume=%s/app/front:/f\n' "$T/pool" >"$T/quadlets/front.container"
printf 'After=app-db.service\n' >"$T/quadlets/lone.container"
: >"$T/quadlets/other.container"
mkdir -p "$T/quadlets/other.container.d"
printf '[Unit]\nWants=app-db.service\n' >"$T/quadlets/other.container.d/app-x.conf"
(. "$T/common"; db_clients) >"$T/out" 2>&1
want "a restore stops whatever needs a database, however far off" '[ "$(tr " " "\n" <"$T/out" | sort | xargs)" = "app.service front.service other.service" ]'

printf '%s\n' app-db.container app.container front.container app.network >"$T/site-units"
(. "$T/common"; drive_paths) >"$T/out" 2>/dev/null
want "the drive takes every volume of the site but a database's" '[ "$(sort "$T/out" | xargs)" = "$T/pool/app/front $T/pool/app/media" ]'
printf 'DATABASES=app=db\nBACKUP_DRIVE_PATHS=app/media\n' >"$T/etc/citrine.conf"
(. "$T/common"; drive_paths) >"$T/out" 2>&1
want "unless BACKUP_DRIVE_PATHS says otherwise" '[ "$(cat "$T/out")" = "$T/pool/app/media" ]'

printf '#!/bin/bash\necho "nft $*" >>"%s/calls"\n' "$T" >"$T/bin/nft"
chmod +x "$T/bin/nft"
sed -e "s|/usr/libexec/citrine/common|$T/common|" -e "s|^subuid=.*|subuid=$T/subuid|" -e "s|^uids=.*|uids=$T/run/uids.nft|" "$X/containment" >"$T/containment"
printf '#!/bin/bash\n[ "$*" = "-un 1001" ] && echo renamed || echo 0\n' >"$T/bin/id"
printf 'core:524288:65536\nrenamed:589824:65536\n1001:700000:10\nrenamed:1:0\ncitrine:900000:65536\n' >"$T/subuid"
: >"$T/calls"
PATH="$T/bin:$PATH" bash "$T/containment" >"$T/out" 2>&1
rc=$?
want "containment covers the uid and every sub-uid range of whoever holds it, and no one else's" '[ $rc = 0 ] && [ "$(cat "$T/run/uids.nft")" = "define user_uids = { 1001, 589824-655359, 700000-700009 }" ] && grep -q "nft -f" "$T/calls"'
: >"$T/subuid"
PATH="$T/bin:$PATH" bash "$T/containment" >"$T/out" 2>&1
want "and still the uid alone when it has none" '[ "$(cat "$T/run/uids.nft")" = "define user_uids = { 1001 }" ]'

cat >"$T/bin/podman" <<EOF
#!/bin/bash
echo "podman \$*" >>"$T/calls"
[ ! -e "$T/psql-fails" ] || exit 1
echo "-- PostgreSQL database dump complete"
EOF
cat >"$T/bin/restic" <<EOF
#!/bin/bash
echo "restic \$*" >>"$T/calls"
while [ \$# -gt 0 ]; do
  [ "\$1" != --target ] || { mkdir -p "\$2/var/tmp" && echo dump >"\$2/var/tmp/citrine-app.sql"; }
  shift
done
exit 0
EOF
printf '#!/bin/bash\necho "systemctl $*" >>"%s/calls"\n' "$T" >"$T/bin/systemctl"
chmod +x "$T/bin"/*
printf 'DATABASES=app=db\n' >"$T/etc/citrine.conf"
rm -f "$T/etc/site.conf"
printf '[Unit]\nRequires=app-db.service\n' >"$T/quadlets/client.container"
sed -e "s|/usr/libexec/citrine/common|$T/common|" -e "s|/etc/citrine/|$T/etc/|g" -e "s|^dumpdir=.*|dumpdir=$T/dumps|" "$X/backup" >"$T/backup"
: >"$T/calls"
PATH="$T/bin:$PATH" bash "$T/backup" r2 >"$T/out" 2>&1
rc=$?
want "a dump is written where only root can, and removed after" '[ $rc = 0 ] && [ "$(stat -c %a "$T/dumps" 2>/dev/null || stat -f %Lp "$T/dumps")" = 700 ] && grep -q "backup --tag db $T/dumps/citrine-app.sql" "$T/calls" && [ ! -e "$T/dumps/citrine-app.sql" ]'

sed -e "s|/usr/libexec/citrine/common|$T/common|" "$X/restore-db" >"$T/restore-db"
: >"$T/calls"
touch "$T/psql-fails"
echo yes | PATH="$T/bin:$PATH" bash "$T/restore-db" r2 >"$T/out" 2>&1
rc=$?
want "a restore that fails starts what it stopped" '[ $rc != 0 ] && [ "$(grep -n "^systemctl --user start .*client.service" "$T/calls" | cut -d: -f1)" -gt "$(grep -n "^systemctl --user stop .*client.service" "$T/calls" | cut -d: -f1)" ]'
want "and finds a dump an older backup kept under var/tmp" 'grep -q "podman exec -i db" "$T/calls"'
rm "$T/psql-fails"
: >"$T/calls"
echo yes | PATH="$T/bin:$PATH" bash "$T/restore-db" r2 >"$T/out" 2>&1
rc=$?
want "a restore that works starts them once" '[ $rc = 0 ] && [ "$(grep -c "^systemctl --user start .*client.service" "$T/calls")" = 1 ]'

[ "$fails" = 0 ] && echo "conf: all passed" || { echo "conf: $fails failed"; exit 1; }
