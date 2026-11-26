#!/bin/bash
set -u
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fails=0
rc=0

mkdir -p "$T/bin" "$T/share/src" "$T/state"
cp "$repo/cloudflare/src/index.js" "$T/share/src/"
cp "$repo/cloudflare/cloudflare.config.ts" "$T/share/"
cp -R "$repo/cloudflare/waf" "$T/share/"

cat >"$T/bin/curl" <<EOF
#!/bin/bash
method=GET url= out= data= body= part= file= main=
while [ \$# -gt 0 ]; do
  case \$1 in
    -X) method=\$2; shift ;;
    -o) out=\$2; shift ;;
    --data) data=\${2#@}; shift ;;
    -d) body=\$2; shift ;;
    -F)
      case \$2 in
        metadata=@*) f=\${2#metadata=@}; main=\$(jq -r .main_module "\${f%%;*}") ;;
        *=@*) part=\${2%%=*}; f=\${2#*=@}; f=\${f%%;*}; file=\${f##*/} ;;
      esac
      shift
      ;;
    -H | -w) shift ;;
    http*) url=\$1 ;;
  esac
  shift
done
echo "\$method \$url\${body:+ \$body}" >>"$T/calls"
[ -z "\$part" ] || echo "\$part \$file \$main" >>"$T/modules"
[ -z "\$data" ] || cp "\$data" "$T/body.json"
case "\$method \$url" in
  "GET "*/channels/stable/versions*) cat "$T/chrome-stable" ;;
  "GET "*/channels/canary/versions*) cat "$T/chrome-canary" ;;
  "GET https://product-details.mozilla.org/"*) cat "$T/firefox" ;;
  "GET https://developer.apple.com/"*) cat "$T/apple" ;;
  "GET "*/entrypoint) cp "$T/live.json" "\$out"; cat "$T/live-code" ;;
  "GET "*/workers/routes) echo '{"result":[]}' ;;
  "GET "*/zones/zone1) echo '{"result":{"name":"example.com","account":{"id":"acct"}}}' ;;
  *) echo '{"success":true}' ;;
esac
EOF
chmod +x "$T/bin/curl"

cat >"$T/common" <<EOF
set -euo pipefail
. "$T/env"
log() { printf '%s\n' "\$*"; }
die() { printf '%s\n' "\$*" >&2; exit 1; }
EOF

sed -e "s|/usr/libexec/citrine/common|$T/common|" \
  -e "s|^src=.*|src=$T/share|" \
  -e "s|/var/lib/citrine/|$T/state/|" \
  -e "s|^state=.*|state=$T/state|" \
  "$repo/tree/usr/libexec/citrine/cloudflare" >"$T/cloudflare"

apple() { printf '<rss><channel><title>Releases</title>\n' >"$T/apple" && printf '<item>\n%s\n</item>\n' "$@" >>"$T/apple"; }
reset() {
  rm -f "$T/state/"* "$T/calls" "$T/body.json" "$T/modules"
  printf 'SHIELD=yes\nCF_WORKERS_TOKEN=tok\nCF_ZONE_ID=zone1\n' >"$T/env"
  echo '{"versions":[{"version":"158.0.8127.37"}]}' >"$T/chrome-stable"
  echo '{"versions":[{"version":"160.0.8167.0"}]}' >"$T/chrome-canary"
  echo '{"LATEST_FIREFOX_VERSION":"160.0.1","FIREFOX_NIGHTLY":"162.0a1","FIREFOX_ESR":"153.7.0esr","FIREFOX_ESR_NEXT":""}' >"$T/firefox"
  apple '<title>iOS 27.3 beta 2 v2 (24C5049f)</title>' '<title>iPadOS 28.0 (24A1)</title>' \
    '<title>iOS 27.2.1 (24B156)</title>' '<title>macOS 27.2.1 (26B131)</title>' '<title>iOS 18.7.12 (22H390)</title>'
  echo '{"result":{"id":"rs1","rules":[{"id":"u1","ref":"mine"},{"id":"c1","ref":"citrine-scripts"}]}}' >"$T/live.json"
  echo 200 >"$T/live-code"
}
run() {
  : >>"$T/calls"
  PATH="$T/bin:$PATH" bash "$T/cloudflare" >"$T/out" 2>&1
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
called() { grep -q "$1" "$T/calls"; }
routed() { grep -qF "{\"pattern\":\"$1\",\"script\":\"$2\"}" "$T/calls"; }
rule() { jq -r --arg r "citrine-$1" '.rules[] | select(.ref == $r) | .expression' "$T/body.json"; }

reset
run
want "deploys the shield and the rules" '[ $rc = 0 ] && called "PUT .*/workers/scripts/citrine-shield" && called "PUT .*/entrypoint"'
want "the shield's module is named for its file, and is its main module" \
  '[ "$(sort "$T/modules" | tr "\n" " ")" = "index.js index.js index.js " ]'
want "the shield takes the rest of the zone" 'routed "example.com/*" citrine-shield && routed "*.example.com/*" citrine-shield'
want "keeps the zone's other rules by id" \
  '[ "$(jq -c "[.rules[] | .id // .ref]" "$T/body.json")" = "[\"u1\",\"citrine-agent\",\"c1\"]" ]'
want "updates its own rule in place" '[ "$(jq -r ".rules[] | select(.ref == \"citrine-scripts\") | .id" "$T/body.json")" = c1 ]'
want "every rule blocks, named" '[ "$(jq "[.rules[] | select(.action == \"block\" and (.description | length > 0))] | length" "$T/body.json")" = 2 ]'
want "fills every part, and stays under Cloudflare's limit" \
  '[ "$(jq "[.rules[] | select(.expression) | .expression | select(contains(\"@\") or length > 4096)] | length" "$T/body.json")" = 0 ]'
want "the shared parts land where they're named" 'rule agent | grep -q "WhatsApp/" && rule scripts | grep -q "Googlebot"'
balanced() {
  local r e o c
  for r in agent scripts; do
    e=$(rule "$r" | sed 's/"[^"]*"//g')
    o=${e//[^(]/} c=${e//[^)]/}
    [ "${#o}" = "${#c}" ] || return 1
  done
}
want "every expression's brackets balance" balanced
lets() { rule agent | grep -qF "\"$1.\""; }
iphone() { rule agent | grep -qF "\"iPhone OS $1_\""; }
want "lets through the three latest Chrome and Firefox, and their pre-releases" \
  'lets Chrome/156 && lets Chrome/160 && ! lets Chrome/155 && ! lets Chrome/161 && lets Firefox/158 && lets Firefox/162 && ! lets Firefox/157 && ! lets Firefox/163'
want "and the current Firefox ESR" 'lets Firefox/153 && ! lets Firefox/152 && ! lets Firefox/154'
want "lets through the three latest Safari, 26 back to 18, from iOS releases alone" \
  'lets Version/27 && lets Version/26 && lets Version/18 && ! lets Version/25 && ! lets Version/17 && ! lets Version/28'
want "lets through the same iPhones, each listed once" \
  'iphone 27 && iphone 26 && iphone 18 && ! iphone 25 && ! iphone 17 && [ "$(rule agent | grep -oF "\"iPhone OS 18_\"" | wc -l | tr -d " ")" = 1 ]'

: >"$T/calls"
run
want "an unchanged run deploys nothing" '[ $rc = 0 ] && ! called PUT && ! called POST'

echo '{"versions":[{"version":"159.0.8150.2"}]}' >"$T/chrome-stable"
: >"$T/calls"
run
want "a Chrome release redeploys only the rules" '[ $rc = 0 ] && called "PUT .*/entrypoint" && ! called "workers/scripts" && lets Chrome/157 && ! lets Chrome/156'

reset
echo '{"versions":[{"version":"158.0.8127.37"}]}' >"$T/chrome-canary"
echo '{"versions":[{"version":"159.0.8150.2"}]}' >"$T/chrome-stable"
run
want "a canary behind stable still lets stable through" '[ $rc = 0 ] && lets Chrome/159 && lets Chrome/157'

reset
echo '{"LATEST_FIREFOX_VERSION":"160.0","FIREFOX_NIGHTLY":"162.0a1","FIREFOX_ESR":"140.9.0esr","FIREFOX_ESR_NEXT":"153.0esr"}' >"$T/firefox"
run
want "while two ESRs overlap, both get through" '[ $rc = 0 ] && lets Firefox/140 && lets Firefox/153 && ! lets Firefox/141'

reset
echo '{"LATEST_FIREFOX_VERSION":"160.0","FIREFOX_NIGHTLY":"162.0a1","FIREFOX_ESR":"159.0esr"}' >"$T/firefox"
run
want "an ESR already in range is listed once" '[ $rc = 0 ] && [ "$(rule agent | grep -oF "\"Firefox/159.\"" | wc -l | tr -d " ")" = 1 ]'

reset
apple '<title>iOS 28 beta (25A5011f)</title>' '<title>iOS 27.5 (24F10)</title>'
run
want "a Safari beta gets through beside the three latest" \
  '[ $rc = 0 ] && lets Version/28 && lets Version/27 && lets Version/26 && lets Version/18 && ! lets Version/17'

reset
apple '<title>iOS 28.0.1 (25A12)</title>' '<title>iOS 27.6.1 (24G3)</title>'
run
want "a Safari release ages out the oldest" '[ $rc = 0 ] && lets Version/28 && lets Version/26 && ! lets Version/18 && ! lets Version/25'
want "but never iPhone OS 18, which every iPhone since 26 reports" 'iphone 28 && iphone 26 && iphone 18 && ! iphone 25'

reset
echo '{"success":false,"errors":[{"message":"not found"}]}' >"$T/live.json"
echo 404 >"$T/live-code"
run
want "makes the entry point when the zone has none" \
  '[ $rc = 0 ] && called "POST .*/zones/zone1/rulesets$" && [ "$(jq -r "[.phase, (.rules | length)] | join(\" \")" "$T/body.json")" = "http_request_firewall_custom 2" ]'

reset
echo '{"success":false,"errors":[{"message":"Authentication error"}]}' >"$T/live.json"
echo 403 >"$T/live-code"
run
want "a token without Zone WAF Edit says so, and keeps no record" \
  '[ $rc != 0 ] && grep -q "Zone WAF Edit" "$T/out" && grep -q "Authentication error" "$T/out" && [ ! -e "$T/state/shield-waf-deployed" ]'
want "but the shield is still deployed" '[ -e "$T/state/shield-deployed" ]'

reset
echo '{}' >"$T/firefox"
run
want "no Firefox version from Mozilla leaves the rules alone" '[ $rc != 0 ] && ! called "entrypoint" && grep -q "no Firefox version" "$T/out"'

reset
echo 'not json' >"$T/chrome-stable"
run
want "no Chrome version from Google leaves the rules alone" '[ $rc != 0 ] && ! called "entrypoint" && grep -q "no Chrome version" "$T/out"'

reset
apple '<title>iOS 28 beta (25A5011f)</title>'
run
want "no iOS release from Apple leaves the rules alone" '[ $rc != 0 ] && ! called "entrypoint" && grep -q "no iOS version" "$T/out"'

reset
printf 'SHIELD=yes\nCF_WORKERS_TOKEN=\nCF_ZONE_ID=zone1\n' >"$T/env"
run
want "no token, no calls" '[ $rc = 0 ] && [ ! -s "$T/calls" ]'

reset
echo SHIELD=no >>"$T/env"
run
want "with the shield off, nothing deploys" \
  '[ $rc = 0 ] && ! called PUT && ! called POST && [ ! -e "$T/state/shield-deployed" ]'

if [ "$fails" = 0 ]; then echo "cloudflare: all passed"; else
  echo "cloudflare: $fails failed"
  exit 1
fi
