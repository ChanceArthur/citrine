#!/bin/bash
set -eu

usage="usage: ${0##*/} [--tag <version>] [output.iso]"
tag=
while [ $# -gt 0 ]; do
  case $1 in
    --tag) tag=${2:?--tag wants a version} && shift ;;
    -*) echo "$usage" >&2 && exit 64 ;;
    *) break ;;
  esac
  shift
done

target=${1:-citrine-install.iso}
out=$(cd "$(dirname "$target")" && pwd)/$(basename "$target")

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

cd "$(dirname "$0")/../.."
. tree/usr/lib/citrine/defaults.conf

[ -z "$tag" ] || IMAGE=${IMAGE%:*}:$tag

: >"$work/site.conf"

hash=$(openssl passwd -6 "$USER_NAME")

sed -e "s|@PASSWORD_HASH@|$hash|" \
  -e "s|@IMAGE@|$IMAGE|" \
  -e "s|@USER_NAME@|$USER_NAME|g" \
  -e "s|@HOSTNAME@|$HOSTNAME|" \
  ignition/citrine.bu.in >"$work/citrine.bu"

podman run --rm -i -v "$work:/data:z" quay.io/coreos/butane:release \
  --pretty --strict --files-dir /data <"$work/citrine.bu" >"$work/citrine.ign"

cp ignition/pick-disk.sh "$work/"

podman run --rm -v "$work:/data:z" -w /data quay.io/coreos/coreos-installer:release \
  download -s stable -p metal -f iso

live=$(ls "$work"/fedora-coreos-*.iso)

podman run --rm -v "$work:/data:z" -w /data quay.io/coreos/coreos-installer:release \
  iso customize \
  --dest-device /dev/sda \
  --dest-ignition citrine.ign \
  --pre-install pick-disk.sh \
  -o citrine-install.iso \
  "$(basename "$live")"

mv "$work/citrine-install.iso" "$out"
echo "citrine: $out"
echo "citrine: installs $IMAGE, then waits for setup"
[ -z "$tag" ] ||
  echo "citrine: pinned — that machine will not update until it is moved back to latest"
echo "citrine: write it with  sudo dd if=$out of=/dev/diskN bs=4m status=progress"
