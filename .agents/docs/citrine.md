# Citrine

`Containerfile` layers `tree/` and the dashboard onto
Fedora CoreOS: one image, every server's. A server takes its site from
`SITE_DIR` in `SITE_REPO` at runtime — `citrine-site` fetches it and applies it
with the machine's own secrets — and `.github/build/iso.sh` wraps a Fedora CoreOS
live ISO around an Ignition config that installs the image and says where the
site is.

## The image and the site

- **The image holds no site and no credential.** A secret or a site's unit in
  `tree/` reaches every machine. `site apply` is the one place a
  site's files reach `/etc`.
- **A site's files in `/etc` are the machine's own, not the image's**, so an
  update never touches them and only a sync changes them. The flip side: a
  file an older image shipped where `site apply` now writes is deleted by the
  update's `/etc` merge and stays gone until the next sync.
- **The site's quadlets share the quadlet directory with the image's**, and
  `site-units` in `/var/lib/citrine` is how a sync tells its own from them. A
  name the site drops is stopped and deleted; one it shares with the image
  overrides it.
- **A sync restarts a container when its own file changed or a changed file is
  named in it** — an env file, a network. A change that reaches a container any
  other way needs a restart by hand.
- **The applied marker is the site's commit and the image's version**, so an
  update that changes what `site apply` writes re-applies an unchanged site.
- **`secrets.env` is sourced as root, so `secrets` is the only thing that writes
  it**: it single-quotes every value and refuses a key nothing declares. A `$` in a
  pasted value stays a `$`. Don't add a verb or `look` case that reads a value back:
  the setup login reaches `secrets` through `setup` before `finish`.
- **A sync can't see a change to a secret**, because the applied marker is the
  commit and the image. `secrets` runs `site apply` itself, and a failed apply stays
  failed until the next change, push or image, with the env files as they were.
- **Push access to a site's repository is the login user on the server, not root**:
  its units run as that user, and `conf.sh` reads its `citrine.conf` as data. Keep it
  so: a value in `conf_keys` reaches root scripts unquoted, so a new key needs its own
  check in `conf_fits`, and a key that could reach root as a value (`IMAGE`,
  `BACKUP_KEEP`, which lands on restic's command line) stays in `site.conf`.
- **GitHub's host key is pinned in `known_hosts`**, not learned. If GitHub ever
  rotates it, every sync fails until an image carries the new one.
- **`load` reads the machine's `site.conf` last**, after the repository's
  conf, so the machine — not the repository it fetches — says where to fetch
  from.
- **`defaults.conf`'s fixed values are spelled out literally** in the image's
  units, rules, `smb.conf`, sudoers and Ignition. Changing one is changing all
  of them.

## What the server builds

- Every container but a site's `BUILD` is a registry image, so no unit can
  depend on a checkout. `AutoUpdate=registry` goes on everything that may
  update unattended.
- **A build reads git's objects, not the checkout**: the checkout is sparse and
  holds only `SITE_DIR`. `git archive` of the folder's tree is unpacked by the
  user, never root, and the tree's hash is the image's `citrine.tree` label,
  which is how a sync knows the image is current even with no new commit.
- **Setup and first boot sync with `--no-build`**: the user's systemd may not be
  up yet, and a failed build there would read as a failed restore and open the
  LAN. `citrine-site.service` at boot builds what they skipped.
- **The build runs under `systemd-run --slice` _and_ `--cgroup-parent`**, both
  `citrine-build.slice`: the first holds podman, the second the containers its
  `RUN` steps start, which otherwise land in `app.slice`. The user's manager is
  delegated cpu, memory and pids but not io, so the slice can't weigh IO.
- **The context is unpacked under `umask 022`**: `render` leaves the sync at
  077, and `COPY` keeps the modes it finds, so an image's own non-root user
  couldn't read its files.
- **`podman untag` given only an image strips every name it has**, `latest`
  included; the names to drop come after the image.
- **A sync fetches again after applying and loops until nothing is new**: a
  start that lands mid-sync merges into the running job, and would be lost.
- **`citrine-site-poll` is the only way a commit reaches the server**; there is no
  webhook. It compares `SITE_REF` on the remote with the applied marker and starts
  the sync. It skips the lock, or it would queue behind a long build. A
  commit whose sync failed waits before the poll retries it, or a broken commit
  would rebuild every tick; a newer commit retries at once.
- **No Containerfile may use a heredoc**: the runner's older podman reads
  `COPY <<'EOF'` as a filename. Use `printf '%s\n'`.
- **The image installs with `dnf`, not `rpm-ostree install`**: from Fedora 45,
  CoreOS's base image has no `/sysroot`, so rpm-ostree can't tell it's in a
  container build and refuses ("not booted via libostree") —
  [fedora-coreos-tracker#2213](https://github.com/coreos/fedora-coreos-tracker/issues/2213).
- **`cloudflared`'s health check is `CMD …`, not a plain string**: a string runs
  through a shell its image doesn't have. Its metrics are pinned to loopback
  because in a container they otherwise listen on all of `edge`, and
  `Notify=healthy` is what lets `podman auto-update` roll back an image that never
  connects.

## The pool survives; `/var` does not

- Root, `/var`, and the rest of the internal disk as the first member of a btrfs
  pool labelled `citrine-data`. Only the pool holds anything irreplaceable.
- `citrine-pool` asks **"is there a pool anywhere"**, not "is this a new
  machine", which is the whole recovery story — never address the pool by
  partition.
- **It waits for `btrfs device ready` before answering.** A USB drive is often
  still spinning up at `local-fs.target`, and answering early either mounts a
  whole pool degraded or makes an empty new one beside the real one.
- In `citrine-pool-heal`, **`device add` runs before `device delete missing`**:
  a degraded pool can't drop the missing device until something holds its copy.
  It is off the boot path because the rebuild takes hours.
- **`mirror_pool` looks for any profile that isn't RAID1**, not for RAID1
  being present: a degraded mount writes `single` chunks, and they stay
  unmirrored after the heal unless something converts them.
- **Root makes only a pool path's top folder; the user makes the rest**
  (`pool_dir`). A container can leave a symlink in its own volume, and root's
  `install -d -o` follows it out of the pool to a user-owned folder anywhere.
  It never makes a top folder starting `citrine-` or a dot: those are root's, and
  a user-owned `citrine-apps` would hand the user the records root trusts. The
  same goes for anything root writes under the user's home: write it as the
  user. And `citrine-app` reads a bundle only from root's own `cp -R` of the
  user's staging folder: `chown -R` alone leaves an image's world-writable modes.
- The directory pass only creates what is missing: `install -d` over an
  adopted pool would hand Postgres's subuid-owned data back to the user.
- `chattr +C` on a database's volumes only at creation — it does nothing to
  existing files, so a volume made before its container joined `DATABASES`
  keeps copy-on-write.
- **The `pool` share refuses connections until `.pool-ready` exists**: `smb`
  isn't ordered after the pool, and a write to an unmounted `/var/mnt/data`
  lands on the internal disk's `/var`.
- Every tree a container mounts is `:Z`, a private MCS label per container. A
  file _moved_ between trees over the share keeps the old tree's label, and the
  new tree's container can't open it; copy instead.
- Partition sizes are fixed MiB in `citrine.bu.in` (Ignition has no percent);
  a too-small disk fails at install.
- **The adopt rule matches `SUBSYSTEMS=="usb"`, never `ID_BUS`**: a USB
  enclosure that passes the drive's ATA identity through gets `ID_BUS=ata`, so
  the one kind of drive the pool wants never fires the rule.
- **`POOL_ADOPT_POLICY=blank`** adopts only a disk with no partition table or
  signature — a big USB disk is also the installer, a backup, or someone's
  photos. The backup label is refused at any policy.

## Things that look simpler than they are

- **`citrine-data.service` is a user unit** that waits for a marker _on_ the
  pool: rootless containers can't order against a system unit, and podman would
  happily create a missing volume path on the internal disk.
- **The user has a fixed uid** because FCOS's `core` takes the first one, and the
  quadlet directory is `users/<uid>/`, not `users/` (which hands the containers
  to every user).
- **`render` and `video` are joined in `first-boot`, not Ignition**: FCOS keeps
  them in `/usr/lib/group`, which `useradd --root /sysroot` can't see, so
  naming them in `citrine.bu.in` fails the install in the initramfs.
- **A Node container runs under `RunInit=true`**: node as PID 1 ignores
  `SIGTERM`, so every stop waits out podman's ten seconds for a `SIGKILL`.
- **Don't `deploy` straight after a boot.** Stopping a unit still waiting on
  `Notify=healthy` hangs until systemd's stop timeout, once per unit, and the
  boot has already pulled `latest`.
- **CI has no deploy job, on purpose**: under Tailnet Lock the node an OAuth
  client mints is never signed, so its `ssh` times out. `podman-auto-update`
  pulls instead. Don't add one back without a pre-signed key, which expires.
- **`SERVE` and `SERVE_TCP` are applied by `first-boot` only.** A new pair
  reaches a running server through `sudo tailscale serve --bg --https=<port>
  <port>` (or `--tcp=<port> tcp://localhost:<port>`) by hand, and no container
  can reach it: `containment.nft` rejects anything a rootless container opens to
  a tailnet address.
- **`containment.nft` also refuses the user's new connections to the host's own
  sshd and smb**: a container reaches them through loopback, where firewalld's
  zones never apply. Anything the user runs on the host is refused them too.
- **`containment.nft` matches the user's sub-uids as well as its uid**: a
  container on the host's network that runs as anyone but root inside, and
  anything the user starts in a user namespace, opens its sockets as a sub-uid.
  `citrine-containment` reads the ranges from `/etc/subuid` at boot, so a range
  added later is uncontained until a reboot.
- **`FedoraServer.xml` drops `ssh` from the LAN zone on purpose**; `sshd` still
  answers on `tailscale0`, which `first-boot` puts in `trusted`. Don't open 22
  on the LAN for a "Tailscale is down" fallback — that's the keyboard's job.
  **It is the LAN's zone only because the Containerfile makes it the
  default**: CoreOS's firewalld defaults to `public`, which allows `ssh`.
- `.github/build/iso.sh` passes `--dest-device` and ignores it: a destination must
  be set for the ISO to install at all; `pick-disk.sh`'s config sorts last and
  wins.

## Credentials

- **The GHCR credential is written twice**: by every sync into
  `/etc/ostree/auth.json` and into the user's own `~/.config/containers/auth.json`
  — rootless podman ignores `/etc`, and `podman login` writes to a tmpfs. The
  `citrine` image must stay public, or a new machine can't rebase. **Linger is
  `first-boot`'s, after the first sync**, not a tmpfiles line: a user manager
  started earlier pulls a site's private images with no credential and gives up
  on them.
- **An OAuth secret in `TS_AUTHKEY` gets `?ephemeral=false` appended**: the
  keys it mints default to ephemeral, and an ephemeral server is deleted from
  the tailnet once it goes offline. The OAuth client itself must carry
  `tag:server`, or the join fails with "requested tags are invalid". Under
  Tailnet Lock the node it joins is unsigned: sign it from a signing node
  before anything can reach a reinstalled server.
- Tailscale joins already tagged; tagging a live node re-authenticates it out
  from under your session, so the policy's `ssh` rules must be saved first.
- **`RESTIC_PASSWORD` is the one secret nothing can reissue.**
- **Test `GH_TOKEN` against `ghcr.io`, never `api.github.com`**: a cloud
  session's egress proxy swaps its own credential in there, so the expiry and
  scopes that come back are the session's.
- A second machine with the real tunnel token joins the live tunnel and takes
  half the traffic.

## Updates

- **Zincati is masked, not dead weight**: CoreOS registers it as rpm-ostree's
  update driver, and while it runs every `rpm-ostree upgrade`, `rebase` and
  `rollback` — the dashboard's included — refuses without `--bypass-driver`. It
  can't update Citrine anyway: the image isn't in Fedora's update graph.
- **The stream is the `FROM` tag alone**: the server follows Citrine's
  `latest`, so a switch needs no rebase.
- **A pinned version is a stopped version**: `rpm-ostreed` upgrades the ref it
  was rebased onto, and a dated tag never moves. That is what
  `.github/build/iso.sh --tag` is for, and why the ISO job never pins.
- `ARG VERSION` sits below the package layer so a new version doesn't rerun it.
- **The version label and the `created` annotation are what `rpm-ostree
  status` prints.** Without them it shows CoreOS's version and build time —
  podman copies the base's annotations — and a fresh Citrine reads as weeks
  old.
- **The timer drop-in clears `OnBootSec` and `OnUnitInactiveSec`** as well as
  `OnCalendar`: rpm-ostree's own timer runs daily and an hour after every
  boot, and with `apply` each of those is a reboot.
- **The OS workflow's cron runs hours ahead of the Saturday window on
  purpose.** GitHub starts scheduled runs late — one landed six hours late —
  and a build that misses the window waits a week.
- **A dead `GH_TOKEN` stops every GHCR pull, the public `citrine` image's
  included**: GHCR answers a bad credential with a 403 instead of falling back
  to anonymous, so OS and app updates both fail on "Requesting bearer token".
  Set the new one on the System tab, then update from the dashboard.
- **`AutomaticUpdatePolicy=apply` is the owner's choice**: the weekly update
  reboots as soon as it stages. `citrine-reboot.timer` isn't dead weight — it
  reboots into an update or rebase staged by hand from the dashboard.
- **`citrine-boot-check` rolls back only onto the deployment it last saw come up
  healthy**, so it never rolls a fresh install back to plain CoreOS and never
  ping-pongs. It judges only what is local: a rollback on a network blip undoes a
  good update. A deployment that never reaches userspace is the keyboard's —
  the previous entry in GRUB — and a bad `latest` is retried every Saturday until
  a fixed one is published.

## Signing

- **`policy.json` checks the image whatever the ref's prefix**: skopeo applies it
  to `ostree-unverified-registry:` too, so once a server has it, a tag pushed
  before signing began can't be pulled — a machine pinned to one stays put.
- **`ostree-image-signed:` refuses while the policy's _default_ is
  `insecureAcceptAnything`**, which is why the default is `reject` and every
  transport is opened by name. A pull on a transport the policy doesn't name
  fails.
- **`matchRepository`, not the default identity**: a podman signature names the
  tag it was pushed under, and the default would refuse `latest` signed as its
  dated tag. It also means GHCR push rights can move `latest` back to any older
  signed build — signing stops a forged image, not a downgrade.
- **The install's rebase is unverified**: plain CoreOS has no policy to check
  with. `citrine-signed-ref` moves a machine onto `ostree-image-signed:` the first
  time it boots a Citrine that has one.
- **The public key is in `/usr`, so only a build can change it**, and a build is
  checked against the key the server already has. Rotating takes one build signed
  by the old key that carries the new one; without the old key, every server's
  `policy.json` has to be pointed at the new key by hand.
- **The signing secrets belong in the `release` environment, which only `main`
  can use.** A repository-level secret of the same name is readable by a
  workflow run from any branch, so the environment protects nothing until the
  repository-level copies are deleted.
- **Every signed push leaves the old `.sig` manifest untagged** — a signature is
  its own package version, `sha256-<digest>.sig` — so prune deletes untagged
  versions and keeps a signature exactly as long as its image.

## Setup

- **The setup login has a public password, so it holds nothing until `finish`.** It
  isn't in `wheel`; `ForceCommand` gives LAN sessions only the setup screen; and
  sudoers lets it run `setup`, whose verbs refuse once setup is done. Giving it
  anything else before `finish` hands the machine to the LAN.
- **`setup boot` opens the LAN with runtime firewall rules only**, so a reboot can
  never leave it open, and it runs only while both `setup.done` and
  `first-boot.done` are absent. A machine set up before `setup.done` existed has
  only the second, and `first-boot` writes both.
- **Nothing during setup may run `firewall-cmd --reload`**: it drops the runtime
  rule, and new connections with it. `first-boot` may, because the LAN is closed by
  then.
- **The record on the pool is what keeps a reinstall from reopening with the public
  password**, so `site sync` refreshes it every time. It never holds `USER_NAME`: a
  reinstall makes `citrine` whatever the old machine was called, and naming a user
  who doesn't exist breaks every `as_user`. It holds the secrets in the clear, like
  `/etc`, but not the share's password.
- **A pool with data and no record is still the public password's to claim**, which
  the screen warns about. It can't happen after the first sync.
- **The tailnet check reads `SSH_CONNECTION`'s server address**, which sshd sets. `F`
  on the screen is the way out if Tailscale SSH doesn't set it.
- **The screen asks for secrets over the tailnet, not before**: a LAN session trusts
  an SSH host key it just learned, which is how a pasted credential gets intercepted.
  `F` carries on from the LAN anyway, for the same reason it finishes from there.
- **A browser login joins untagged**, because a tag needs the policy's owner. So
  `TS_TAGS` applies only to a pasted key and `TS_AUTHKEY`.

## Apps

- **An app's own unit carries its wiring, and a target's goes in a drop-in.**
  `site apply` owns the target's file and rewrites it; it never touches
  `<unit>.container.d/`. It finds what to restart by grepping `*.container`, so
  an app's `EnvironmentFile=` has to be in its own file for a rotated secret to
  restart it.
- **Units live in `/etc/containers/systemd/users/<uid>`, not `~/.config`**: the
  dashboard lists that directory and nothing else.
- **An app's record lives on the pool, owned by root**, because `restore` replays
  it as root. The `pool` share writes as the user, who can't touch it.
- **Ostree's `/etc` merge deletes a file identical to one the image used to
  ship.** Installing an app whose unit matches a default the next update removes
  loses it.

## Backups

- **The dumps of `DATABASES` and `BACKUP_PATHS` go to R2 nightly;
  `BACKUP_DRIVE_PATHS` to a drive plugged in now and then.** Both restic, one
  password. The drive's default leaves out a database's volumes: a copy of a
  live data directory doesn't restore, and the dumps already hold it.
- **A restore's clients follow dependencies, not `After=`**: ordering alone
  isn't a client. An app that `--join`s a database's container `Wants=` it, so
  a restore stops it too.
- **A dump is taken with the database container's own `POSTGRES_DB` and
  `POSTGRES_USER`**, so a database in `DATABASES` is any container from the
  `postgres` images, and the base reads no database credential.
- **`pg_dump`, never a copy of the data directory**, and the dump is checked for
  its completion trailer — a truncated dump only fails on the day it's needed.
- **The restore rewrites `search_path` in every dump with `sed`** — Immich's
  sets an empty one and fails to replay without it.
- **A dump's file name is its `DATABASES` name**, and `restore-db` looks for
  those names in the newest snapshot: renaming one leaves nothing to restore
  until the next backup.
- **`restore-db` still looks under `var/tmp` in a snapshot**: dumps were written
  there before they moved to a root-only folder, and those snapshots are the ones
  an old backup holds.
- **`BACKUP_LABEL` stays within eleven characters, uppercase**: that is FAT32's and
  exFAT's limit, and Windows and Disk Utility uppercase a FAT32 label while
  udev matches it case-sensitively.
- **`forget` groups by tag, not restic's default of paths**: every new volume or
  app path is a new set of paths, and under the default each old set keeps its
  snapshots for good.
- **`RESTIC_CACHE_DIR` in `common` is load-bearing**: a system unit has no
  `$HOME`, and without it every restic call fails — including the "is there a
  repo" probe, so only exit 10 may lead to `restic init`.
- **R2 needs an S3 key pair from _Manage R2 API Tokens_**, scoped to the bucket —
  a general API token is a bearer token restic can't use.
  `AWS_DEFAULT_REGION=auto` or signatures can fail.
