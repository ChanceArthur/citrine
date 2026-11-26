#!/bin/bash
# Runs in the live environment, before coreos-installer. Names the disk to
# install onto by writing an installer config, which is the supported way to
# choose a destination the ISO could not know when it was built.
set -euo pipefail

exec > >(tee /dev/console) 2>&1
echo "citrine: choosing an install disk"

best='' best_kind='' best_rota=2 best_size=0
while read -r line; do
  for key in NAME SIZE TYPE TRAN RM ROTA MODEL; do
    value=
    [[ $line =~ (^|[[:space:]])$key=\"([^\"]*)\" ]] && value=${BASH_REMATCH[2]}
    printf -v "$key" '%s' "$value"
  done
  [ "$TYPE" = disk ] || continue
  [ "$RM" = 0 ] || continue
  case "$TRAN" in usb|"") continue ;; esac
  [ -z "$(lsblk -no MOUNTPOINTS "/dev/$NAME" | tr -d '[:space:]')" ] || continue
  kind=$([ "$ROTA" = 0 ] && echo ssd || echo hdd)
  echo "  candidate /dev/$NAME  $((SIZE / 1000000000)) GB  $kind  $TRAN  ${MODEL:-}"
  [ "$ROTA" -lt "$best_rota" ] ||
    { [ "$ROTA" = "$best_rota" ] && [ "$SIZE" -gt "$best_size" ]; } || continue
  best=$NAME best_kind=$kind best_rota=$ROTA best_size=$SIZE
done < <(lsblk -dbP -o NAME,SIZE,TYPE,TRAN,RM,ROTA,MODEL)

if [ -z "$best" ]; then
  echo "citrine: no fixed internal disk found — nothing to install onto" >&2
  exit 1
fi

echo "citrine: installing onto /dev/$best ($((best_size / 1000000000)) GB $best_kind)"
mkdir -p /etc/coreos/installer.d
sed -i '/^dest-device:/d' /etc/coreos/installer.d/*.yaml 2>/dev/null || true
cat > /etc/coreos/installer.d/99-citrine.yaml <<EOF
dest-device: /dev/$best
EOF
