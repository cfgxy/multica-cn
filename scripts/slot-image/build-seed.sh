#!/usr/bin/env bash
# build-seed.sh — render the autoinstall seed (user-data / meta-data /
# seed-manifest.json) for the multica-slot-base image.
#
# Mechanics mirror the validated RUYI-454/RUYI-679 preparation: subiquity
# picks the seed from an attached FAT32 volume labelled CIDATA (made on the
# Hyper-V host by host/mk-cidata.ps1), installs unattended, and powers off —
# host/install-vm.ps1 waits for that, host/finalize-base.ps1 moves the disk
# to the read-only base and folds seed-manifest.json into the image manifest.
#
# Package posture (方案 §4.1): PostgreSQL + node runtime + the guard /
# inventory / artifact-install guest scripts embedded at
# /usr/local/lib/multica/ + light debug tools. NO build toolchain.
#
# Usage:
#   build-seed.sh --out <dir> --pubkey-file <acceptance.pub> \
#                 [--image-version <v>] [--guest-dir <dir>]
# A one-shot console rescue password is generated into <dir>/.console-password
# (mode 600; only its $6$ hash lands in user-data). <dir>/ is a build output —
# never commit it.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
. "$here/versions.env"

out_dir="" pubkey_file="" image_version="" guest_dir="$here/guest"
while [ $# -gt 0 ]; do
  case "$1" in
    --out) out_dir=$2; shift 2 ;;
    --pubkey-file) pubkey_file=$2; shift 2 ;;
    --image-version) image_version=$2; shift 2 ;;
    --guest-dir) guest_dir=$2; shift 2 ;;
    *) printf 'build-seed: unknown flag %s\n' "$1" >&2; exit 2 ;;
  esac
done
[ -n "$out_dir" ] || { printf 'build-seed: --out <dir> required\n' >&2; exit 2; }
[ -n "$pubkey_file" ] && [ -f "$pubkey_file" ] || { printf 'build-seed: --pubkey-file <acceptance.pub> required\n' >&2; exit 2; }
image_version="${image_version:-$(date -u '+%Y.%m.%d')}"
pubkey="$(cat "$pubkey_file")"
# Fingerprint-grade anchor (RUYI-727): the seed manifest records WHICH public
# key the image authorizes so a bake can be reconciled against the pool's
# production key without reading the image or touching private material.
# pubkey_sha256 covers the base64 body text (no ssh-keygen dependency, byte
# exact; trailing newline stripped via command substitution); the SSH
# fingerprint is best effort (absent for malformed keys).
pubkey_body="$(head -n1 "$pubkey_file" | awk '{print $2}')"
pubkey_sha256="$(printf '%s' "$pubkey_body" | sha256sum | awk '{print $1}')"
pubkey_fp=""
if command -v ssh-keygen >/dev/null 2>&1; then
  pubkey_fp="$(ssh-keygen -lf "$pubkey_file" 2>/dev/null | awk '{print $2}')" || pubkey_fp=""
fi

mkdir -p "$out_dir"

# --- console rescue password (hash only in user-data) ------------------------
pw_file="$out_dir/.console-password"
if [ ! -f "$pw_file" ]; then
  openssl rand -base64 18 2>/dev/null | head -c 18 >"$pw_file" ||
    printf 'slot-image-console-%s\n' "$(head -c 8 /dev/urandom | base64)" >"$pw_file"
  chmod 600 "$pw_file"
fi
pw_hash="$(openssl passwd -6 -salt "sb$(openssl rand -hex 4)" "$(cat "$pw_file")")"

# --- guest scripts: embed byte-identical via base64 late-commands ------------
guest_cmds=""
guest_entries=""
for g in guard-scan.sh artifact-install.sh slot-inventory.sh; do
  f="$guest_dir/$g"
  [ -f "$f" ] || { printf 'build-seed: guest script missing: %s\n' "$f" >&2; exit 1; }
  b64="$(base64 -w0 "$f")"
  guest_cmds+="    - curtin in-target --target=/target -- bash -c \"mkdir -p /usr/local/lib/multica && echo '$b64' | base64 -d > /usr/local/lib/multica/$g && chmod 755 /usr/local/lib/multica/$g\""$'\n'
  hash="$(sha256sum "$f" | awk '{print $1}')"
  guest_entries+=$(printf '{"name":"%s","sha256":"%s"},' "$g" "$hash")
done
guest_entries="${guest_entries%,}"

# --- user-data ---------------------------------------------------------------
cat >"$out_dir/user-data" <<EOF
#cloud-config
autoinstall:
  version: 1
  locale: en_US.UTF-8
  keyboard:
    layout: us
  network:
    network:
      version: 2
      ethernets:
        eth0:
          dhcp4: true
  storage:
    layout:
      name: direct
      match:
        size: largest
  identity:
    hostname: ${SLOT_IMAGE_HOSTNAME}
    username: ${SLOT_IMAGE_USER}
    password: "$pw_hash"
  ssh:
    install-server: true
    allow-pw: true
    authorized-keys:
      - "$pubkey"
  packages:
    - linux-cloud-tools-virtual
    - linux-tools-virtual
    - postgresql
    - postgresql-client
    - jq
    - curl
    - unzip
    - git
    - less
    - tree
    - lsof
    - ca-certificates
  late-commands:
    - curtin in-target --target=/target -- bash -c "echo '${SLOT_IMAGE_USER} ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/90-${SLOT_IMAGE_USER} && chmod 440 /etc/sudoers.d/90-${SLOT_IMAGE_USER}"
    - curtin in-target --target=/target -- bash -c "curl -fsSL https://deb.nodesource.com/setup_${NODE_MAJOR}.x | bash - && apt-get install -y nodejs"
    - curtin in-target --target=/target -- bash -c "systemctl enable fstrim.timer"
    - curtin in-target --target=/target -- bash -c "useradd -m -d /home/agent -s /bin/bash agent && chmod 750 /home/agent"
$guest_cmds  shutdown: poweroff
  user-data:
    disable_root: true
EOF

# --- meta-data ---------------------------------------------------------------
printf 'instance-id: %s-%s\n' "$SLOT_IMAGE_HOSTNAME" "$image_version" >"$out_dir/meta-data"

# --- seed manifest (folded into the image manifest by finalize-base.ps1) -----
iso_sha="${UBUNTU_ISO_SHA256}"
node_major="${NODE_MAJOR}"
pg_major="${POSTGRES_MAJOR}"
jq -n \
  --arg image "$SLOT_IMAGE_HOSTNAME" \
  --arg version "$image_version" \
  --arg ubuntu "$UBUNTU_VERSION" \
  --arg iso_name "$UBUNTU_ISO_NAME" \
  --arg iso_sha "$iso_sha" \
  --argjson node "$node_major" \
  --argjson pg "$pg_major" \
  --arg identity_user "$SLOT_IMAGE_USER" \
  --arg pk_sha "$pubkey_sha256" \
  --arg pk_fp "$pubkey_fp" \
  --arg guest_entries "[$guest_entries]" \
  '{schema: "multica-slot-base-seed v1", image: $image, version: $version,
    versions: {ubuntu: $ubuntu, iso_name: $iso_name, iso_sha256: $iso_sha,
               node_major: $node, postgres_major: $pg},
    identity: {user: $identity_user, pubkey_sha256: $pk_sha,
               pubkey_fingerprint: (if $pk_fp == "" then null else $pk_fp end)},
    guest_scripts: ($guest_entries | fromjson)}' \
  >"$out_dir/seed-manifest.json"

printf 'build-seed: wrote %s/{user-data,meta-data,seed-manifest.json} (image %s@%s)\n' \
  "$out_dir" "$SLOT_IMAGE_HOSTNAME" "$image_version"
