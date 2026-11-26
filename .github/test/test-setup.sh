#!/bin/bash
set -u
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fails=0

mkdir -p "$T/bin" "$T/libexec"

log_stub() {
  printf '#!/bin/bash\necho "%s $*" >>"%s/calls"\n' "$1" "$T" >"$T/bin/$1"
}
for c in usermod hostnamectl systemctl resolvectl systemd-run firewall-cmd; do log_stub "$c"; done
cat >"$T/bin/chpasswd" <<EOF
#!/bin/bash
echo "chpasswd \$(cat)" >>"$T/calls"
EOF
cat >"$T/bin/smbpasswd" <<EOF
#!/bin/bash
cat >/dev/null
echo "smbpasswd \$*" >>"$T/calls"
EOF
cat >"$T/bin/id" <<'EOF'
#!/bin/bash
echo 0
EOF
cat >"$T/bin/mountpoint" <<'EOF'
#!/bin/bash
exit 0
EOF
cat >"$T/bin/getent" <<EOF
#!/bin/bash
echo "me:\\\$6\\\$fakehash:1:2:3"
EOF
cat >"$T/bin/ip" <<'EOF'
#!/bin/bash
echo "default via 1.1.1.1 dev eth0"
EOF
cat >"$T/bin/hostname" <<'EOF'
#!/bin/bash
echo 192.0.2.1
EOF
cat >"$T/bin/ssh-keygen" <<EOF
#!/bin/bash
case "\$*" in
  *-y*) echo "ssh-ed25519 AAAAFAKE" ;;
  *) f=\${@: -1}; echo PRIVATE >"\$f"; echo PUB >"\$f.pub"; chmod 600 "\$f" ;;
esac
EOF
cat >"$T/bin/tailscale" <<EOF
#!/bin/bash
echo "tailscale \$*" >>"$T/calls"
case "\$1" in
  status) printf '{"BackendState":"%s","AuthURL":"","Self":{"DNSName":"box.tail.ts.net."}}\n' "\$(cat "$T/ts")" ;;
  lock) if [ -e "$T/unsigned" ]; then echo '{"Enabled":true,"NodeKeySigned":false,"NodeKey":"nodekey:abc"}'; else echo '{"Enabled":false}'; fi ;;
esac
exit 0
EOF
cat >"$T/bin/install" <<'EOF'
#!/bin/bash
args=()
while [ $# -gt 0 ]; do
  case $1 in -o | -g) shift ;; *) args+=("$1") ;; esac
  shift
done
exec /usr/bin/install "${args[@]}"
EOF
sed --version >/dev/null 2>&1 || cp "$here/sed-i.py" "$T/bin/sed"
chmod +x "$T"/bin/*

cat >"$T/libexec/site" <<EOF
#!/bin/bash
echo "site \$*" >>"$T/calls"
[ ! -e "$T/site-fails" ] || exit 1
EOF
cat >"$T/libexec/secrets" <<EOF
#!/bin/bash
echo "secrets \$* \$(cat)" >>"$T/calls"
EOF
chmod +x "$T/libexec/site" "$T/libexec/secrets"

cat >"$T/common" <<EOF
set -euo pipefail
POOL_MOUNT=$T/pool; USER_NAME=me; USER_UID=1001; HOSTNAME=citrine; TIMEZONE=Etc/UTC; TS_TAGS=tag:server
load() { . "$T/defaults"; local f; for f in "\$@" $T/etc/site.conf; do if [ -r "\$f" ]; then . "\$f"; fi; done; }
log() { printf '%s\n' "\$*"; }
die() { printf '%s\n' "\$*" >&2; exit 1; }
load
EOF
printf "HOSTNAME=citrine\nTIMEZONE=Etc/UTC\nSHIELD=yes\n" >"$T/defaults"

sed -e "s|/usr/libexec/citrine/common|$T/common|" \
  -e "s|/usr/libexec/citrine/site|$T/libexec/site|g" \
  -e "s|/usr/libexec/citrine/secrets|$T/libexec/secrets|g" \
  -e "s|^conf=.*|conf=$T/etc/site.conf|" \
  -e "s|^key=.*|key=$T/etc/site.key|" \
  -e "s|^state=.*|state=$T/state|" \
  -e "s|^dropin=.*|dropin=$T/ssh/10-citrine-setup.conf|" \
  -e "s|^mdns=.*|mdns=$T/run/citrine-setup.conf|" \
  -e "s|/etc/citrine/citrine.conf|$T/etc/citrine.conf|g" \
  -e "s|/etc/citrine/secrets.env|$T/etc/secrets.env|g" \
  -e "s|install -d /etc/ssh/sshd_config.d|install -d $T/ssh|" \
  -e "s|install -d -m 0755 /etc/citrine|install -d -m 0755 $T/etc|" \
  "$repo/tree/usr/libexec/citrine/setup" >"$T/setup"

reset() {
  rm -rf "${T:?}/state" "${T:?}/etc" "${T:?}/pool" "$T/ssh" "$T/run" "$T/unsigned" "$T/site-fails"
  mkdir -p "$T/state" "$T/etc" "$T/pool" "$T/ssh" "$T/run"
  echo Stopped >"$T/ts"
}
run() {
  : >"$T/calls"
  PATH="$T/bin:$PATH" bash "$T/setup" "$@" >"$T/out" 2>&1 </dev/null
  rc=$?
}
runin() {
  local input=$1
  shift
  : >"$T/calls"
  printf '%s\n' "$input" | PATH="$T/bin:$PATH" bash "$T/setup" "$@" >"$T/out" 2>&1
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
before() { [ "$(grep -n -- "$1" "$T/calls" | head -1 | cut -d: -f1)" -lt "$(grep -n -- "$2" "$T/calls" | head -1 | cut -d: -f1)" ]; }

ready() {
  reset
  runin 'a-long-password' password
  run hostname box
  run timezone America/New_York
  run features no
  run site git@github.com:me/site.git
  echo Running >"$T/ts"
}

reset
run boot
want "a fresh boot opens ssh, for this boot only" '[ $rc = 0 ] && called "firewall-cmd --add-service=ssh" && ! grep -q "permanent.*ssh" "$T/calls"'
want "the setup login can only run setup" 'grep -q "ForceCommand /usr/bin/dashboard --setup" "$T/ssh/10-citrine-setup.conf" && grep -q "AllowTcpForwarding no" "$T/ssh/10-citrine-setup.conf" && grep -q "PermitTunnel no" "$T/ssh/10-citrine-setup.conf"'
want "only the setup login gets the restriction, and global options come first" '[ "$(sed -n 1p "$T/ssh/10-citrine-setup.conf")" = "PasswordAuthentication yes" ] && grep -q "^Match User me" "$T/ssh/10-citrine-setup.conf"'
want "the screen names the temporary password" 'grep -q "first password is me" "$T/out"'

for tag in setup.done first-boot.done; do
  reset
  touch "$T/state/$tag"
  for verb in boot status password hostname timezone features tailscale site check secrets finish; do
    run "$verb"
    want "$verb refuses after $tag" '[ $rc != 0 ] && ! called usermod && ! called "add-service"'
  done
done

reset
runin 'short' password
want "refuses a short password" '[ $rc != 0 ] && ! called chpasswd && [ ! -e "$T/state/setup-password" ]'
reset
runin 'me' password
want "refuses the temporary password" '[ $rc != 0 ] && ! called chpasswd'
reset
runin 'me-long-password' password
want "sets the login and the share" '[ $rc = 0 ] && called "chpasswd me:me-long-password" && called "smbpasswd -s -a me" && [ -e "$T/state/setup-password" ]'
want "doesn't print the password" '! grep -q me-long-password "$T/out"'

reset
for bad in 'a;b' 'UPPER' '-x' 'a b' '$(id)'; do
  run hostname "$bad"
  want "refuses the hostname $bad" '[ $rc != 0 ] && ! called hostnamectl && [ ! -s "$T/etc/site.conf" ]'
done
run hostname box
want "sets the name for the machine and for later loads" '[ $rc = 0 ] && called "hostnamectl set-hostname box" && grep -q "^HOSTNAME=box$" "$T/etc/site.conf"'
run hostname box2
want "replaces it rather than adding another" '[ "$(grep -c ^HOSTNAME= "$T/etc/site.conf")" = 1 ] && grep -q "^HOSTNAME=box2$" "$T/etc/site.conf"'

reset
for bad in 'Not/AZone' '../../etc/passwd' 'a b' '$(id)' ''; do
  run timezone "$bad"
  want "refuses the time zone $bad" '[ $rc != 0 ] && [ ! -s "$T/etc/site.conf" ] && [ ! -e "$T/state/setup-timezone" ]'
done
run timezone America/New_York
want "sets the time zone for later loads" '[ $rc = 0 ] && grep -q "^TIMEZONE=America/New_York$" "$T/etc/site.conf"'
run timezone Etc/UTC
want "replaces it rather than adding another" '[ "$(grep -c ^TIMEZONE= "$T/etc/site.conf")" = 1 ]'

reset
run status
want "the shield starts on" '[ $rc = 0 ] && grep -qx FEATURES_SET=no "$T/out" && grep -qx SHIELD=yes "$T/out"'
for bad in 'maybe' '' '$(id)' 'true'; do
  run features "$bad"
  want "refuses the shield choice $bad" '[ $rc != 0 ] && [ ! -s "$T/etc/site.conf" ] && [ ! -e "$T/state/setup-features" ]'
done
run features no
run status
want "keeps the choice for later loads" '[ $rc = 0 ] && grep -qx FEATURES_SET=yes "$T/out" && grep -qx SHIELD=no "$T/out"'
run features yes
want "replaces it rather than adding another" '[ "$(grep -c ^SHIELD= "$T/etc/site.conf")" = 1 ] && grep -qx SHIELD=yes "$T/etc/site.conf"'

reset
runin '' tailscale
want "no key means a browser login" '[ $rc = 0 ] && called "systemd-run.*tailscale up --hostname=citrine --ssh" && ! called "advertise-tags"'
want "joining never reloads the firewall, which would close this session" '! called "firewall-cmd --reload" && called "firewall-cmd --zone=trusted --change-interface=tailscale0"'
reset
runin 'tskey-auth-secret' tailscale
want "a key stays off the command line" '[ $rc = 0 ] && called "auth-key=file:" && ! called tskey-auth-secret && called "advertise-tags=tag:server"'
reset
runin 'not-a-key' tailscale
want "refuses something that isn't a key" '[ $rc != 0 ] && ! called "tailscale up"'

reset
for bad in 'http://x/y.git' 'git@x:y;rm' '--upload-pack=x' 'git@x:$(id)'; do
  run site "$bad"
  want "refuses the repository $bad" '[ $rc != 0 ] && [ ! -e "$T/etc/site.key" ]'
done
run site git@github.com:me/site.git
want "makes a key and keeps only the private half" '[ $rc = 0 ] && [ -e "$T/etc/site.key" ] && [ ! -e "$T/etc/site.key.pub" ] && grep -q "^SITE_REPO=git@github.com:me/site.git$" "$T/etc/site.conf"'
echo CHANGED >"$T/etc/site.key"
run site git@github.com:me/other.git
runin 'SITE_TOKEN=pasted' secrets set
want "secrets go to the script on stdin, off the command line" '[ $rc = 0 ] && called "secrets set SITE_TOKEN=pasted"'
want "changing the address keeps the key" '[ "$(cat "$T/etc/site.key")" = CHANGED ] && [ "$(grep -c ^SITE_REPO= "$T/etc/site.conf")" = 1 ]'

reset
run site https://github.com/me/site.git
want "a public repository needs no key" '[ $rc = 0 ] && [ ! -e "$T/etc/site.key" ] && grep -q "^SITE_REPO=https://github.com/me/site.git$" "$T/etc/site.conf"'
for bad in 'https://user:token@github.com/me/site.git' 'https://x/y;z'; do
  run site "$bad"
  want "refuses the address $bad" '[ $rc != 0 ]'
done

reset
echo Running >"$T/ts"
run finish
want "finish refuses without a password" '[ $rc != 0 ] && [ ! -e "$T/state/setup.done" ]'
ready
rm "$T/state/setup-timezone"
run finish
want "finish refuses without a time zone" '[ $rc != 0 ] && [ ! -e "$T/state/setup.done" ]'
ready
rm "$T/state/setup-features"
run finish
want "finish refuses before the features are chosen" '[ $rc != 0 ] && [ ! -e "$T/state/setup.done" ]'
ready
echo Stopped >"$T/ts"
run finish
want "finish refuses off the tailnet" '[ $rc != 0 ] && [ ! -e "$T/state/setup.done" ] && ! called "usermod -aG wheel"'
ready
touch "$T/unsigned"
run finish
want "finish refuses an unsigned Tailnet Lock node" '[ $rc != 0 ] && [ ! -e "$T/state/setup.done" ]'
ready
touch "$T/site-fails"
run finish
want "finish refuses a site it can't read" '[ $rc != 0 ] && [ ! -e "$T/state/setup.done" ] && ! called "usermod -aG wheel"'

ready
PATH="$T/bin:$PATH" bash "$T/setup" boot >/dev/null 2>&1
run finish
want "finish grants sudo, closes ssh and starts first boot" '[ $rc = 0 ] && called "usermod -aG wheel me" && [ -e "$T/state/setup.done" ] && [ ! -e "$T/ssh/10-citrine-setup.conf" ] && called "firewall-cmd --remove-service=ssh" && called "systemctl start --no-block citrine-first-boot.service"'
want "finish tries the site's env files before granting anything" 'before "site render" "usermod -aG wheel"'
want "sudo comes before anything closes" 'before "usermod -aG wheel" "remove-service=ssh"'
want "finish leaves a record, without the login name" '[ -f "$T/pool/citrine-setup/hash" ] && [ -f "$T/pool/citrine-setup/site.key" ] && ! grep -q USER_NAME "$T/pool/citrine-setup/site.conf"'
want "the record is root's alone" '[ "$(stat -c %a "$T/pool/citrine-setup" 2>/dev/null || stat -f %Lp "$T/pool/citrine-setup")" = 700 ]'

reset
run save
want "save never records the temporary password" '[ ! -e "$T/pool/citrine-setup" ]'
echo x >"$T/etc/site.key"; echo USER_NAME=old >"$T/etc/site.conf"; touch "$T/state/first-boot.done"
run save
want "save works on a machine that's already set up, and drops an old login name" '[ $rc = 0 ] && [ -f "$T/pool/citrine-setup/hash" ] && ! grep -q USER_NAME "$T/pool/citrine-setup/site.conf"'

ready
echo "TS_AUTHKEY='tskey-auth-x'" >"$T/etc/secrets.env"
run finish
want "the record keeps the secrets, for root alone" 'grep -q TS_AUTHKEY "$T/pool/citrine-setup/secrets.env" && [ "$(stat -c %a "$T/pool/citrine-setup/secrets.env" 2>/dev/null || stat -f %Lp "$T/pool/citrine-setup/secrets.env")" = 600 ]'
cp -R "$T/pool" "$T/kept"
reset
rm -rf "$T/pool"; mv "$T/kept" "$T/pool"
run boot
want "a reinstall gets its secrets back from the pool" '[ "$(stat -c %a "$T/etc/secrets.env" 2>/dev/null || stat -f %Lp "$T/etc/secrets.env")" = 600 ] && grep -q TS_AUTHKEY "$T/etc/secrets.env"'
want "a reinstall with a tailnet key stays closed" '[ $rc = 0 ] && [ -e "$T/state/setup.done" ] && [ ! -e "$T/ssh/10-citrine-setup.conf" ] && ! called "add-service=ssh" && called "usermod -aG wheel me" && [ -e "$T/etc/site.key" ]'
want "and restores the real password" 'called "usermod -p"'
want "and keeps its time zone without asking" 'grep -q "^TIMEZONE=America/New_York$" "$T/etc/site.conf" && [ -e "$T/state/setup-timezone" ]'
want "and its features" 'grep -qx SHIELD=no "$T/etc/site.conf" && [ -e "$T/state/setup-features" ]'

ready
run finish
cp -R "$T/pool" "$T/kept"
reset
rm -rf "$T/pool"; mv "$T/kept" "$T/pool"
run boot
want "a reinstall without one opens ssh behind the real password" '[ $rc = 0 ] && [ ! -e "$T/state/setup.done" ] && [ -e "$T/ssh/10-citrine-setup.conf" ] && [ -e "$T/state/setup-password" ] && called "usermod -p" && ! grep -q "first password" "$T/out"'

ready
run finish
cp -R "$T/pool" "$T/kept"
reset
rm -rf "$T/pool"; mv "$T/kept" "$T/pool"
touch "$T/site-fails"
run boot
want "a reinstall that can't reach the site opens ssh, and doesn't finish" '[ $rc = 0 ] && [ ! -e "$T/state/setup.done" ] && [ -e "$T/ssh/10-citrine-setup.conf" ]'

reset
run site https://github.com/me/site.git
runin 'a-long-password' password
run hostname box
run timezone Etc/UTC
run features yes
echo Running >"$T/ts"
run finish
want "a machine with no key can still finish and leave a record" '[ $rc = 0 ] && [ -f "$T/pool/citrine-setup/hash" ] && [ ! -e "$T/pool/citrine-setup/site.key" ]'
cp -R "$T/pool" "$T/kept"
reset
rm -rf "$T/pool"; mv "$T/kept" "$T/pool"
run boot
want "and is restored from it" '[ $rc = 0 ] && called "usermod -p" && [ ! -e "$T/etc/site.key" ]'

[ "$fails" = 0 ] && echo "setup: all passed" || { echo "setup: $fails failed"; exit 1; }
