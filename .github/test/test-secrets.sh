#!/bin/bash
set -u
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
L=$repo/tree/usr/libexec/citrine
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fails=0

mkdir -p "$T/bin" "$T/libexec"
cat >"$T/bin/systemctl" <<EOF
#!/bin/bash
echo "systemctl \$*" >>"$T/calls"
case "\$*" in
  "show -P Result "*) cat "$T/result" 2>/dev/null || echo success ;;
  "show -P InactiveEnterTimestamp "*) cat "$T/inactive" 2>/dev/null ;;
esac
EOF
printf '#!/bin/bash\necho 0\n' >"$T/bin/id"
printf '#!/bin/bash\nexit 0\n' >"$T/bin/flock"
printf '#!/bin/bash\necho abc123\n' >"$T/bin/git"
cat >"$T/bin/install" <<'EOF'
#!/bin/bash
args=()
while [ $# -gt 0 ]; do
  case $1 in -o | -g) shift ;; *) args+=("$1") ;; esac
  shift
done
exec /usr/bin/install "${args[@]}"
EOF
cat >"$T/bin/restic" <<EOF
#!/bin/bash
echo "restic \$* key=\${AWS_ACCESS_KEY_ID:-} pw=\${RESTIC_PASSWORD:-}" >>"$T/calls"
[ ! -e "$T/restic-fails" ] || exit 1
cat "$T/dump"
EOF
chmod +x "$T"/bin/*

cat >"$T/libexec/site" <<EOF
#!/bin/bash
echo "site \$*" >>"$T/calls"
[ ! -e "$T/site-fails" ] || exit 1
EOF
cat >"$T/libexec/setup" <<EOF
#!/bin/bash
echo "setup \$*" >>"$T/calls"
EOF
chmod +x "$T/libexec"/*

sed -e "s|/usr/lib/citrine/|$repo/tree/usr/lib/citrine/|g" "$repo/tree/usr/lib/citrine/conf.sh" >"$T/conf.sh"
cat >"$T/common" <<EOF
set -euo pipefail
SITE_DIR=citrine; TIMEZONE=America/New_York; POOL_MOUNT=$T/pool; USER_UID=1001; USER_NAME=me
. $T/conf.sh
load() { conf_read "\${1:-}"; [ \$# = 0 ] || shift; local f; for f in "\$@"; do if [ -r "\$f" ]; then . "\$f"; fi; done; }
log() { printf '%s\n' "\$*"; }
die() { printf '%s\n' "\$*" >&2; exit 1; }
as_user() { echo "as_user \$1" >>"$T/calls"; "\$@"; }
r2_env() { export AWS_ACCESS_KEY_ID=\${R2_ACCESS_KEY_ID:-} RESTIC_PASSWORD=\${RESTIC_PASSWORD:-}; export RESTIC_REPOSITORY=x; }
EOF
sed -n '/^unit_volumes()/,/^}/p' "$L/common" >>"$T/common"

sed -e "s|/usr/libexec/citrine/common|$T/common|" \
  -e "s|/usr/libexec/citrine/site|$T/libexec/site|" \
  -e "s|/usr/libexec/citrine/setup|$T/libexec/setup|" \
  -e "s|^file=.*|file=$T/etc/secrets.env|" \
  -e "s|^state=.*|state=$T/state|" \
  -e "s|^own=.*|own=$repo/tree/usr/lib/citrine/secrets.conf|" \
  "$L/secrets" >"$T/secrets"

sed -e "s|/usr/libexec/citrine/common|$T/common|" \
  -e "s|^state=.*|state=$T/state|" \
  -e "s|^key=.*|key=$T/etc/site.key|" \
  -e "s|/etc/citrine/|$T/etc/|g" \
  -e "s|/etc/localtime|$T/localtime|g" \
  -e "s|/etc/ostree/|$T/etc/|g" \
  -e "s|/var/home/|$T/home/|" \
  -e "s|^quadlets=.*|quadlets=$T/quadlets|" \
  -e "s|trap 'rm -rf \"\$stage\"' EXIT|trap 'rm -rf $T/rendered; cp -R \"\$stage/env\" $T/rendered; rm -rf \"\$stage\"' EXIT|" \
  "$L/site" >"$T/site"

reset() {
  rm -rf "${T:?}/state" "${T:?}/etc" "${T:?}/dump" "$T/site-fails" "$T/restic-fails"
  mkdir -p "$T/state/site/citrine/env" "$T/etc"
  printf 'SITE_URL=https://example.com\nDB_USER=\n' >"$T/state/site/citrine/citrine.conf"
  printf '# @IN_A_COMMENT@\nA=@SITE_TOKEN@\nB=postgres://@DB_USER@:@DB_PASSWORD@@db/@SITE_URL@\nC=@TIMEZONE@\n\nT=@TUNNEL_TOKEN@\n' >"$T/state/site/citrine/env/web.env"
  printf 'P=@DB_PASSWORD@\n' >"$T/state/site/citrine/env/db.env"
  printf 'X=@FROM_CITRINES_OWN@\n' >"$T/state/site/citrine/env/cloudflared.env"
}
run() {
  : >"$T/calls"
  PATH="$T/bin:$PATH" bash "$T/secrets" "$@" >"$T/out" 2>&1 </dev/null
  rc=$?
}
runin() {
  local input=$1
  shift
  : >"$T/calls"
  printf '%s\n' "$input" | PATH="$T/bin:$PATH" bash "$T/secrets" "$@" >"$T/out" 2>&1
  rc=$?
}
want() {
  if eval "$2"; then
    echo "ok    $1"
  else
    echo "FAIL  $1 (rc=$rc)"
    sed 's/^/      /' "$T/out" "$T/calls"
    fails=$((fails + 1))
  fi
}
called() { grep -q -- "$1" "$T/calls"; }
value() { (. "$T/etc/secrets.env" && eval "printf '%s' \"\$$1\""); }

reset
run list
want "lists Citrine's keys, then the site's" '[ $rc = 0 ] && grep -q "^K TUNNEL_TOKEN empty$" "$T/out" && grep -q "^K SITE_TOKEN empty$" "$T/out"'
want "a key both declare is listed once" '[ "$(grep -c "^K TUNNEL_TOKEN " "$T/out")" = 1 ]'
want "a key two templates ask for is listed once" '[ "$(grep -c "^K DB_PASSWORD " "$T/out")" = 1 ]'
want "and under the first template that asks for it" '[ "$(grep -B1 "^K DB_PASSWORD " "$T/out" | head -1)" = "G env/db.env" ]'
want "a template's keys are grouped under its name" '[ "$(grep -B1 "^K SITE_TOKEN " "$T/out" | head -1)" = "G env/web.env" ]'
want "a key in a template's comment isn't asked for" '! grep -q "^K IN_A_COMMENT " "$T/out"'
want "the conf's keys and the machine's aren't secrets" '! grep -qE "^K (SITE_URL|DB_USER|TIMEZONE) " "$T/out"'
want "a template Citrine overwrites declares nothing" '! grep -q FROM_CITRINES_OWN "$T/out"'

for k in PATH IFS BASH_ENV LD_PRELOAD USER_NAME POOL_MOUNT SITE_REPO RESTIC_PASSWORD_COMMAND; do
  reset
  printf '# Mine.\nA=@SITE_TOKEN@ @%s@\n' "$k" >"$T/state/site/citrine/env/web.env"
  runin "$k=/tmp/x" set
  want "a site can't declare $k, which root's scripts or the machine read" '[ $rc != 0 ] && [ ! -e "$T/etc/secrets.env" ]'
done
run list
want "and the rest of its keys still count" '[ $rc = 0 ] && grep -q "^K SITE_TOKEN empty$" "$T/out" && ! grep -q "^K RESTIC_PASSWORD_COMMAND" "$T/out"'

reset
runin "SITE_TOKEN=first-value" set
run list
want "list says what's set, and never its value" 'grep -q "^K SITE_TOKEN set$" "$T/out" && ! grep -q first-value "$T/out"'
want "the file is root's alone" '[ "$(stat -c %a "$T/etc/secrets.env" 2>/dev/null || stat -f %Lp "$T/etc/secrets.env")" = 600 ]'

for v in "it's" 'a$b' 'x;y z' '$(touch '"$T"'/pwned)' 'back\slash' '`touch '"$T"'/pwned`' 'a"b' 'p%sq'; do
  reset
  runin "SITE_TOKEN=$v" set
  want "sourcing keeps the value exactly: $v" '[ $rc = 0 ] && [ "$(value SITE_TOKEN)" = "$v" ] && [ ! -e "$T/pwned" ]'
done

reset
runin "SITE_TOKEN='quoted value'" set
want "strips one pair of single quotes" '[ "$(value SITE_TOKEN)" = "quoted value" ]'
runin 'SITE_TOKEN="double"' set
want "strips one pair of double quotes" '[ "$(value SITE_TOKEN)" = double ]'
runin "$(printf 'export DB_PASSWORD=pw\r')" set
want "takes export and a Windows line ending" '[ "$(value DB_PASSWORD)" = pw ]'
runin 'SITE_TOKEN=' set
want "an empty value keeps what's there" '[ "$(value SITE_TOKEN)" = double ]'
runin 'SITE_TOKEN=new' set
want "a new value replaces the old, once" '[ "$(value SITE_TOKEN)" = new ] && [ "$(grep -c ^SITE_TOKEN= "$T/etc/secrets.env")" = 1 ] && [ "$(value DB_PASSWORD)" = pw ]'

reset
runin 'SITE_TOKEN=kept
UNDECLARED=top-secret-value' set
want "refuses a key nothing declares, and writes nothing" '[ $rc != 0 ] && [ ! -e "$T/etc/secrets.env" ] && grep -q UNDECLARED "$T/out" && ! grep -q top-secret-value "$T/out"'
runin 'just-a-pasted-secret' set
want "a line that isn't KEY=value is named by number, not printed" '[ $rc != 0 ] && grep -q "line 1" "$T/out" && ! grep -q just-a-pasted-secret "$T/out"'
runin 'lower=abc' set
want "refuses a lowercase key" '[ $rc != 0 ] && [ ! -e "$T/etc/secrets.env" ]'

reset
runin 'SITE_TOKEN=x' set
want "during setup a change writes the file and applies nothing" '[ $rc = 0 ] && ! called "site apply"'
touch "$T/state/setup.done"
runin 'SITE_TOKEN=y' set
want "after setup a change applies, records and redeploys" 'called "site apply" && called "setup save" && called "citrine-cloudflare"'
touch "$T/site-fails"
runin 'SITE_TOKEN=z' set
want "a failed apply is reported, with the value kept" '[ $rc != 0 ] && [ "$(value SITE_TOKEN)" = z ]'
rm -f "$T/site-fails"
run unset SITE_TOKEN
want "unset removes it and applies" '[ $rc = 0 ] && ! grep -q ^SITE_TOKEN= "$T/etc/secrets.env" && called "site apply"'
run unset 'A;B'
want "unset refuses a bad name" '[ $rc != 0 ]'

reset
echo "OLD_KEY='x'" >"$T/etc/secrets.env"
run list
want "a key nothing declares any more can still be seen and removed" 'grep -q "^K OLD_KEY set$" "$T/out"'

reset
printf "SITE_TOKEN='from-backup'\nRESTIC_PASSWORD='old'\nR2_BUCKET='old-bucket'\n" >"$T/dump"
runin 'R2_BUCKET=new-bucket
R2_ACCOUNT_ID=acct
R2_ACCESS_KEY_ID=akey
RESTIC_PASSWORD=pw' restore
want "restore opens the backup with what was pasted" '[ $rc = 0 ] && called "dump --tag files latest $T/etc/secrets.env" && called "key=akey pw=pw"'
want "restore brings the rest back, and what was pasted wins" '[ "$(value SITE_TOKEN)" = from-backup ] && [ "$(value R2_BUCKET)" = new-bucket ] && [ "$(value RESTIC_PASSWORD)" = pw ]'
reset
touch "$T/restic-fails"
runin 'RESTIC_PASSWORD=wrong' restore
want "a backup that won't open is reported" '[ $rc != 0 ] && grep -q "newest backup" "$T/out" && ! grep -q wrong "$T/out"'
reset
printf "SITE_TOKEN='it'\\\\''s'\n" >"$T/dump"
runin 'RESTIC_PASSWORD=pw' restore
want "a restored value keeps its quote" '[ $rc = 0 ] && [ "$(value SITE_TOKEN)" = "it'"'"'s" ]'
for bad in "SITE_TOKEN='x'; touch $T/pwned" "PATH='/tmp'" 'SITE_TOKEN=$(touch '"$T"'/pwned)'; do
  reset
  printf '%s\n' "$bad" >"$T/dump"
  runin 'RESTIC_PASSWORD=pw' restore
  want "a backup's secrets.env that secrets didn't write is refused: $bad" '[ $rc != 0 ] && ! grep -q pwned "$T/etc/secrets.env" && ! grep -q "^PATH=" "$T/etc/secrets.env" && [ ! -e "$T/pwned" ]'
done

want "both backups carry the secrets" '[ "$(grep -c "paths=\"/etc/citrine/secrets.env " "$L/backup")" = 2 ]'

render() {
  : >"$T/calls"
  SITE_REPO=https://example.com/me/site.git PATH="$T/bin:$PATH" bash "$T/site" render >"$T/out" 2>&1
  rc=$?
}
reset
D=$T/state/site/citrine
rm -rf "$D/env"
mkdir -p "$D/env"
printf 'SITE_URL=https://example.com\nDB_USER=me\nDB_NAME=site\n' >"$D/citrine.conf"
cat >"$D/env/web.env" <<'EOF'
# Mail someone@example.com about @NOT_A_KEY@
URL=@SITE_URL@
ZONE=@TIMEZONE@
DB=postgres://@DB_USER@:@SITE_TOKEN@@db:5432/@DB_NAME@
MAIL=someone@example.com
PLAIN=nothing
EOF
printf 'LAST=@DB_USER@' >"$D/env/no-newline.env"
printf 'TUNNEL_TOKEN=from-the-template\n' >"$D/env/cloudflared.env"
printf 'touch %s/pwned\n' "$T" >"$D/mkenv.sh"
got() { grep -q "^$2=$3$" "$T/rendered/$1"; }
echo "SITE_TOKEN='machine'" >"$T/etc/secrets.env"
render
want "fills a key from the secrets, the conf and the machine" '[ $rc = 0 ] && got web.env URL https://example.com && got web.env ZONE America/New_York'
want "fills keys that touch each other, and a literal @ beside them" 'got web.env DB "postgres://me:machine@db:5432/site"'
want "leaves another @ alone" 'got web.env MAIL someone@example.com && got web.env PLAIN nothing'
want "leaves a comment alone" 'grep -q "NOT_A_KEY" "$T/rendered/web.env"'
want "takes a last line with no newline" 'got no-newline.env LAST me'
want "a site can't write Citrine's own env files" '! grep -q from-the-template "$T/rendered/cloudflared.env"'
want "a script in the site never runs" '[ ! -e "$T/pwned" ]'
for v in 'a$b' '$(touch '"$T"'/pwned)' '`touch '"$T"'/pwned`' 'a\b' 'x@DB_USER@y' '&' '%s' 'a b'; do
  q=$(printf '%s' "$v" | sed "s/'/'\\\\''/g")
  printf "SITE_TOKEN='%s'\n" "$q" >"$T/etc/secrets.env"
  render
  want "a value goes in exactly as it is: $v" '[ $rc = 0 ] && [ "$(sed -n "s/^DB=postgres:\/\/me://p" "$T/rendered/web.env")" = "$v@db:5432/site" ] && [ ! -e "$T/pwned" ]'
done
echo "SITE_TOKEN='machine'" >"$T/etc/secrets.env"
echo "SITE_TOKEN=from-the-repository" >"$D/secrets.env"
render
want "a secrets.env committed to the site is ignored" 'got web.env DB "postgres://me:machine@db:5432/site"'
rm -f "$D/secrets.env" "$T/etc/secrets.env"
render
want "a missing secret stops it, and names the key and the file" '[ $rc != 0 ] && grep -q SITE_TOKEN "$T/out" && grep -q web.env "$T/out"'
echo "SITE_TOKEN='machine'" >"$T/etc/secrets.env"
echo 'A=@RESTIC_PASSWORD@' >"$D/env/x.env"
echo "RESTIC_PASSWORD='the-backup-password'" >>"$T/etc/secrets.env"
render
want "a template can't ask for Citrine's own secrets" '[ $rc != 0 ] && grep -q "Citrine.s own" "$T/out" && ! grep -q the-backup-password "$T/out"'
echo "SITE_TOKEN='machine'" >"$T/etc/secrets.env"
for k in PATH USER_NAME POOL_MOUNT; do
  echo "A=@$k@" >"$D/env/x.env"
  render
  want "a template can't read the shell's $k" '[ $rc != 0 ] && grep -q "needs $k" "$T/out"'
done
echo 'A=@conf_keys@' >"$D/env/x.env"
render
want "a lowercase name is only text" '[ $rc = 0 ] && got x.env A @conf_keys@'
echo 'A=@TIMEZONE@' >"$D/env/x.env"
echo "IMAGE=evil" >>"$D/citrine.conf"
render
want "a conf reaching for the machine's keys stops the sync" '[ $rc != 0 ] && grep -q "IMAGE is the machine" "$T/out"'
sed -i.bak '/^IMAGE=/d' "$D/citrine.conf"; rm -f "$D/citrine.conf.bak"
echo "DB_USER='from-the-machine'" >>"$T/etc/secrets.env"
render
want "a value in the machine's secrets beats the conf's" '[ $rc = 0 ] && got web.env DB "postgres://from-the-machine:machine@db:5432/site"'
ln -s /etc/hosts "$D/env/link.env"
render
want "a symlink in the site stops the sync" '[ $rc != 0 ] && grep -q symlink "$T/out"'
rm -f "$D/env/link.env"
ln -s "$T/link-target" "$D/env/link.env"
printf 'A=@STOLEN_LINE@\n' >"$T/link-target"
run list
want "a symlink in the site declares nothing, as it renders nothing" '[ $rc = 0 ] && ! grep -q STOLEN_LINE "$T/out" && sed -n "/^G Nothing declares/,\$p" "$T/out" | grep -q "^K SITE_TOKEN set$"'
rm -f "$D/env/link.env"
if grep -nE '\bage\b|secrets\.recipients|secrets\.env\.age' "$L/site" "$L/setup" "$L/first-boot" >"$T/out"; then rc=1; else rc=0; fi
want "nothing decrypts anything any more" '[ $rc = 0 ]'

apply() {
  SITE_REPO=https://example.com/me/site.git PATH="$T/bin:$PATH" bash "$T/site" apply >"$T/out" 2>&1
  rc=$?
}
Q=$T/quadlets
rm -f "$D/env/x.env"
mkdir -p "$D/units"
printf '[Container]\nVolume=%s/web/data:/data:Z\n' "$T/pool" >"$D/units/web.container"
printf '[Container]\nImage=x\n' >"$D/units/cache.container"
printf '[Network]\n' >"$D/units/web.network"
apply
want "a unit on the pool waits for it" '[ $rc = 0 ] && grep -qx "Requires=citrine-data.service" "$Q/web.container.d/citrine-data.conf" && grep -qx "After=citrine-data.service" "$Q/web.container.d/citrine-data.conf" && grep -q "changed .*web.container" "$T/out"'
want "and the user's quadlet generator can read its drop-in" '[ "$(stat -c %a "$Q/web.container.d" 2>/dev/null || stat -f %Lp "$Q/web.container.d")" = 755 ]'
want "a unit off the pool doesn't" '[ ! -e "$Q/cache.container.d" ] && [ ! -e "$Q/web.network.d" ]'
apply
want "and an unchanged one isn't counted as a change" '[ $rc = 0 ] && ! grep -q "changed" "$T/out"'
printf '[Container]\nImage=x\n' >"$D/units/web.container"
apply
want "a unit that stops using the pool loses it" '[ $rc = 0 ] && [ ! -e "$Q/web.container.d" ] && grep -q "changed web.container" "$T/out"'
printf '[Container]\nVolume=%s/web/data:/data:Z\n' "$T/pool" >"$D/units/web.container"
apply
mkdir -p "$Q/web.container.d" && touch "$Q/web.container.d/app-code.conf"
rm "$D/units/web.container"
apply
want "a removed unit takes it along, and leaves an app's drop-in" '[ $rc = 0 ] && [ ! -e "$Q/web.container" ] && [ ! -e "$Q/web.container.d/citrine-data.conf" ] && [ -e "$Q/web.container.d/app-code.conf" ]'

mkdir -p "$T/home/me"
printf "SITE_TOKEN='x'\nGH_TOKEN='tok'\n" >"$T/etc/secrets.env"
: >"$T/calls"
apply
want "the user's GHCR credential is written as the user, not chowned by root" '[ $rc = 0 ] && grep -q "$(printf x-access-token:tok | base64)" "$T/home/me/.config/containers/auth.json" && cmp -s "$T/home/me/.config/containers/auth.json" "$T/etc/auth.json" && grep -q "^as_user bash" "$T/calls"'

poll() {
  : >"$T/calls"
  SITE_REPO=https://example.com/me/site.git SITE_REF=main PATH="$T/bin:$PATH" bash "$T/site" poll >"$T/out" 2>&1
  rc=$?
}
printf 'abc123 VERSION=1\n' >"$T/state/site-applied"
poll
want "a poll that finds the applied commit starts nothing" '[ $rc = 0 ] && ! called "start"'
printf 'old456 VERSION=1\n' >"$T/state/site-applied"
poll
want "a poll that finds a newer commit starts a sync" '[ $rc = 0 ] && called "systemctl start --no-block citrine-site.service"'
echo failed >"$T/result"
echo "@$(($(date +%s) - 60))" >"$T/inactive"
poll
want "a commit whose sync just failed isn't retried yet" '[ $rc = 0 ] && ! called "start"'
echo "@$(($(date +%s) - 7200))" >"$T/inactive"
poll
want "but is, an hour on" '[ $rc = 0 ] && called "systemctl start --no-block citrine-site.service"'
rm -f "$T/result" "$T/inactive"

[ "$fails" = 0 ] && echo "secrets: all passed" || { echo "secrets: $fails failed"; exit 1; }
