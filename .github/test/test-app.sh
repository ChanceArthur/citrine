#!/bin/bash
set -u
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fails=0

mkdir -p "$T"/bin "$T"/pool "$T"/quadlets "$T"/env "$T"/img/units "$T"/tmp

cat >"$T/bin/podman" <<EOF
#!/bin/bash
case \$1 in
  pull) exit 0 ;;
  create) echo ctr ;;
  cp) cp -R "$T/img/." "\${3%/}/" && echo "\${3%/}" >"$T/stage" ;;
  run) echo "podman run" >>"$T/calls" ;;
esac
EOF
cat >"$T/bin/tailscale" <<EOF
#!/bin/bash
echo "tailscale \$*" >>"$T/calls"
[ ! -e "$T/swap" ] || ln -sf "$T/root-only" "\$(cat "$T/stage")/units/web.container" 2>/dev/null
case \$1 in status) [ "\${2:-}" = --json ] && echo '{"Self":{"DNSName":"box.tail.ts.net."}}' ;; esac
exit 0
EOF
cat >"$T/bin/systemctl" <<EOF
#!/bin/bash
echo "systemctl \$*" >>"$T/calls"
EOF
printf '#!/bin/bash\nexit 0\n' >"$T/bin/mountpoint"
printf '#!/bin/bash\necho "chown $*" >>"%s/calls"\n' "$T" >"$T/bin/chown"
cat >"$T/bin/install" <<'EOF'
#!/bin/bash
args=()
while [ $# -gt 0 ]; do
  case $1 in -o | -g) shift ;; *) args+=("$1") ;; esac
  shift
done
exec /usr/bin/install "${args[@]}"
EOF
cat >"$T/bin/id" <<'EOF'
#!/bin/bash
[ "$1" = -u ] && echo 0 || echo me
EOF
sed --version >/dev/null 2>&1 || cp "$here/sed-i.py" "$T/bin/sed"
chmod +x "$T"/bin/*

cat >"$T/common" <<EOF
set -euo pipefail
POOL_MOUNT=$T/pool; USER_UID=1001; USER_NAME=me; HOSTNAME=box; SERVE="443=2283"; SERVE_TCP="5433=5433"
log() { printf '%s\n' "\$*"; }
die() { printf '%s\n' "\$*" >&2; exit 1; }
as_user() { "\$@"; }
EOF
sed -n '/^pool_dir()/,/^}/p' "$repo/tree/usr/libexec/citrine/common" >>"$T/common"
sed -e "s|/usr/libexec/citrine/common|$T/common|" \
  -e "s|^quadlets=.*|quadlets=$T/quadlets|" \
  -e "s|^envdir=.*|envdir=$T/env|" \
  -e "s|mktemp -d /var/tmp/citrine-app.XXXXXX|mktemp -d $T/tmp/a.XXXXXX|" \
  -e "1s|.*|#!$(command -v bash)|" \
  "$repo/tree/usr/bin/citrine-app" >"$T/app"
chmod +x "$T/app"

cat >"$T/img/app.conf" <<'EOF'
NAME=web
SERVE=9443=9443
BACKUP=web/state
BACKUP_EXCLUDE=web/state/cache
EOF
cat >"$T/img/units/web.container" <<'EOF'
[Unit]
Description=x

[Container]
Image=reg/web:latest
Environment=H=@HOST@,@TAILNET_HOST@
Volume=@POOL@/web/state:/data:Z
EOF
cp "$T/img/app.conf" "$T/conf"
touch "$T/quadlets/cms.container" "$T/quadlets/db.container" "$T/env/site.env"

app() {
  : >"$T/calls"
  PATH="$T/bin:$PATH" "$T/app" "$@" >"$T/out" 2>&1
  rc=$?
}
Q=$T/quadlets

want() {
  if eval "$2"; then
    echo "ok    $1"
  else
    echo "FAIL  $1 (rc=$rc)"
    cat "$T/out" "$T/calls" | sed 's/^/      /'
    fails=$((fails + 1))
  fi
}

refuses() {
  local name=$1
  shift
  app "$@"
  want "refuses $name" '[ $rc != 0 ] && [ ! -e "$T/pool/citrine-apps/web" ]'
}

app show reg/web:latest
want "show prints what it would install" '[ $rc = 0 ] && grep -q "web, from reg/web:latest" "$T/out" && grep -q "^\[Container\]" "$T/out" && [ ! -e "$Q/web.container" ]'
want "and what it keeps out of the backup" 'grep -q "not backed up: web/state/cache" "$T/out"'

app install reg/web:latest --join cms,db --env site.env --yes
want "install succeeds" '[ $rc = 0 ]'
want "the user fills only a staging folder, never the one root reads" '[ "$(grep -c "^chown" "$T/calls")" = 1 ] && grep -q "^chown 1001 $T/tmp/a\." "$T/calls"'
want "the image's code never runs" '! grep -q "podman run" "$T/calls"'
want "placeholders are filled in" 'grep -q "H=box,box.tail.ts.net" "$Q/web.container" && grep -q "Volume=$T/pool/web/state" "$Q/web.container"'
want "one Network line however many it joins" '[ "$(grep -c "^Network=web.network" "$Q/web.container")" = 1 ]'
want "orders after what it joins" 'grep -q "^After=cms.service" "$Q/web.container" && grep -q "^Wants=db.service" "$Q/web.container"'
want "env file goes in its own unit" 'grep -q "^EnvironmentFile=$T/env/site.env" "$Q/web.container"'
want "joined containers get drop-ins, their files untouched" 'grep -q "Network=web.network" "$Q/cms.container.d/app-web.conf" && [ ! -s "$Q/cms.container" ]'
want "the network is created" 'grep -q "NetworkName=web" "$Q/web.network"'
want "pool directories are made" '[ -d "$T/pool/web/state" ]'
want "the tailnet port is served" 'grep -q "tailscale serve --bg --https=9443 9443" "$T/calls"'
want "joined containers restart" 'grep -q "restart --no-block cms.service db.service" "$T/calls"'
want "the record holds the wiring" 'grep -q "^JOIN=cms db$" "$T/pool/citrine-apps/web" && grep -q "^BACKUP=web/state$" "$T/pool/citrine-apps/web"'

app install reg/web:latest --join cms,db --env site.env --yes
want "reinstalling leaves joined containers running" '[ $rc = 0 ] && ! grep -q "cms.service" "$T/calls"'

app list
want "list names it" 'grep -q "^web" "$T/out"'

app restore
want "restore replays the wiring" '[ $rc = 0 ] && [ "$(grep -c "^Network=web.network" "$Q/web.container")" = 1 ] && grep -q "^EnvironmentFile=" "$Q/web.container"'

app remove web
want "remove takes everything off" '[ $rc = 0 ] && [ ! -e "$Q/web.container" ] && [ ! -e "$Q/web.network" ] && [ ! -e "$Q/cms.container.d/app-web.conf" ] && [ ! -e "$T/pool/citrine-apps/web" ]'
want "remove stops serving and keeps the files" 'grep -q "tailscale serve --https=9443 off" "$T/calls" && [ -d "$T/pool/web/state" ]'
want "the site's own units survive" '[ -e "$Q/cms.container" ] && [ -e "$Q/db.container" ]'

touch "$Q/web.container"
refuses "to overwrite a unit it doesn't own" install reg/web:latest --yes
want "and leaves that unit alone" '[ ! -s "$Q/web.container" ]'
rm "$Q/web.container"

refuses "a container that doesn't exist" install reg/web:latest --join nope --yes
refuses "an env file outside the directory" install reg/web:latest --env ../secrets --yes
refuses "a bad image reference" install "reg/web;rm" --yes

conf() {
  echo "$1" >>"$T/img/app.conf"
  refuses "$2" install reg/web:latest --yes
  cp "$T/conf" "$T/img/app.conf"
}
conf 'BACKUP=../../etc' "a backup path out of the pool"
conf 'SERVE=443=80' "a port the site already serves"
conf 'SERVE=x' "a malformed port"
conf 'NAME=../x' "a name with a path in it"
conf 'BACKUP=cms' "a backup path outside the app's own volumes"
conf 'BACKUP_EXCLUDE=other' "an exclusion outside the app's own volumes"
conf 'BACKUP=web/./state' "a path with a dot in it"
conf 'BACKUP_EXCLUDE=[o]ther/data' "a wildcard that matches nothing here, to skip the checks"

cp "$T/img/units/web.container" "$T/web.container"
echo 'Volume=@POOL@/cms/media:/m' >>"$T/img/units/web.container"
conf 'BACKUP_EXCLUDE=cms/media' "an exclusion over a volume it borrowed from someone else"
cp "$T/web.container" "$T/img/units/web.container"
printf 'NAME=x\033[2Jy\n' >>"$T/img/app.conf"
app install reg/web:latest --yes
cp "$T/conf" "$T/img/app.conf"
want "a refusal shows the image's control bytes as text" '[ $rc != 0 ] && grep -q "\^\[\[2J" "$T/out" && ! grep -q "$(printf "\033")" "$T/out"'
conf 'SERVE=9[0-9]=22' "a wildcard in a port"

echo "victim" >"$T/victim"
ln -s "$T/victim" "$T/img/join"
app install reg/web:latest --join cms --yes
rm "$T/img/join"
want "a join file in the bundle can't point root's write elsewhere" '[ $rc = 0 ] && [ "$(cat "$T/victim")" = victim ] && grep -q "Network=web.network" "$Q/cms.container.d/app-web.conf"'
app remove web

cp "$T/img/units/web.container" "$T/web.container"
printf 'PodmanArgs=--x\033[2Kmasked\n' >>"$T/img/units/web.container"
app show reg/web:latest
want "show draws a unit's control bytes as text" '[ $rc = 0 ] && grep -q "\^\[\[2Kmasked" "$T/out" && ! grep -q "$(printf "\033")" "$T/out"'
cp "$T/web.container" "$T/img/units/web.container"

cp "$T/img/units/web.container" "$T/web.container"
echo 'Volume=@POOL@/citrine-apps/x:/x' >>"$T/img/units/web.container"
refuses "a volume in one of Citrine's own pool folders" install reg/web:latest --yes
want "and makes nothing there" '[ ! -e "$T/pool/citrine-apps/x" ]'
cp "$T/web.container" "$T/img/units/web.container"

echo "root-only secret" >"$T/root-only"
touch "$T/swap"
app install reg/web:latest --yes
rm "$T/swap"
want "a swap in the user's copy after the check never reaches the unit" '! grep -q "root-only secret" "$Q/web.container"'
app remove web

cp "$T/img/units/web.container" "$T/web.container"
echo 'Volume=@POOL@/../../etc:/x' >>"$T/img/units/web.container"
refuses "a volume out of the pool" install reg/web:latest --yes
cp "$T/web.container" "$T/img/units/web.container"

ln -s /etc/passwd "$T/img/units/evil.container"
refuses "a symlinked unit" install reg/web:latest --yes
rm "$T/img/units/evil.container"

mv "$T/img/units" "$T/units"
ln -s "$T/units" "$T/img/units"
refuses "a symlinked units folder" install reg/web:latest --yes
rm "$T/img/units" && mv "$T/units" "$T/img/units"

cp "$T/img/units/web.container" "$T/img/units/run.sh"
refuses "a file that isn't a quadlet" install reg/web:latest --yes
rm "$T/img/units/run.sh"

mkdir -p "$T/pool/citrine-apps"
echo "SERVE=9443=1" >"$T/pool/citrine-apps/other"
refuses "a tailnet port another app already serves" install reg/web:latest --yes
rm "$T/pool/citrine-apps/other"

app install reg/web:latest --yes
printf 'NAME=other\n' >"$T/img/app.conf"
mv "$T/img/units/web.container" "$T/img/units/other.container"
app restore
want "a restore refuses an image that changed its name" 'grep -q "now calls itself other, not web" "$T/out" && [ ! -e "$Q/other.container" ] && [ ! -e "$T/pool/citrine-apps/other" ]'
mv "$T/img/units/other.container" "$T/img/units/web.container"
cp "$T/conf" "$T/img/app.conf"
app remove web

refuses "a second image, which the preview never showed" install reg/web:latest reg/other:latest --yes

echo "Volume=$T/pool/web/library:/data:Z" >"$T/quadlets/cms.container"
refuses "backup paths in a folder another container keeps its data in" install reg/web:latest --yes
want "and says whose it is" 'grep -q "is cms.container.s" "$T/out"'
: >"$T/quadlets/cms.container"

printf 'NAME=photos\nBACKUP_EXCLUDE=photos\n' >"$T/img/app.conf"
mv "$T/img/units/web.container" "$T/img/units/photos.container"
BACKUP_PATHS=photos app install reg/web:latest --yes
want "refuses backup paths in a folder the site already backs up" '[ $rc != 0 ] && grep -q "already backed up" "$T/out" && [ ! -e "$T/pool/citrine-apps/photos" ]'
mv "$T/img/units/photos.container" "$T/img/units/web.container"
cp "$T/conf" "$T/img/app.conf"

want "no temporary directory is left" '[ -z "$(ls "$T/tmp")" ]'

[ "$fails" = 0 ] && echo "app: all passed" || { echo "app: $fails failed"; exit 1; }
