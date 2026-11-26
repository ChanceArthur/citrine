# Citrine

A server as an image: Fedora CoreOS plus the pool, the backups, the first
boot, the dashboard and `cloudflared`, published as `citrine` and run by every
server. **It knows no site.** A server fetches its site's `citrine/` — units, conf,
and env templates — from the site's repository at runtime, and keeps its secrets itself.

```bash
./.github/build/os.sh            # citrine:latest, locally
./.github/build/iso.sh           # the install ISO, the same for every server
cd dashboard && go vet ./...     # the dashboard
```

## Before you say it's done

1. `cd dashboard && go vet ./... && gofmt -l .` — clean, and nothing listed.
2. `shellcheck -S warning` on every script you touched — clean.
3. `bash .github/test/test-pool.sh` if you touched `pool`, `usb-adopt` or
   `pool-heal`, `test-app.sh` beside it for `citrine-app`, `test-secrets.sh` for `secrets` and `site`, `test-setup.sh` for
   `setup`, `test-cloudflare.sh` for `cloudflare` and `cloudflare/`, and `test-conf.sh` for `conf.sh`, `common`, `backup`, `restore-db` and `containment` — passing,
   under Homebrew's bash 5 (`PATH=/opt/homebrew/opt/bash/bin:$PATH`): macOS's own 3.2 fails them on empty arrays.
4. **Count the comments you added.** The budget is zero. Spend one only where
   the reason cannot be read off the code and losing it would undo the thing,
   and then write a `.agents/docs/` pointer instead of the argument.
5. **Read back each sentence you added to a `.md` and cut the ones a future
   session could get by reading the code.** What earns a line: a trap, a
   measurement that cost something to find, a constraint that looks like dead
   code, a rule with no trace in the source. **Never copy a value out of the
   code into prose** — the next unrelated tweak falsifies it.

## Read the doc before you edit

| Doc                                                    | Read before touching                                            |
| ------------------------------------------------------ | --------------------------------------------------------------- |
| [.agents/docs/citrine.md](.agents/docs/citrine.md)     | anything in `tree/`, `ignition/`, the build scripts             |
| [.agents/docs/dashboard.md](.agents/docs/dashboard.md) | `dashboard/`, the login session                                 |
| [.agents/docs/cloudflare.md](.agents/docs/cloudflare.md) | `cloudflare/`                                                 |

**`.agents/docs/` and this file are yours to quietly maintain, and correcting
them needs no permission.** Correct what has gone _wrong_, not what has gone
less complete.

## Non-negotiable

- **Nothing site-shaped in the image.** No site's unit, hostname, user, path
  under the pool or credential goes into `tree/` or the dashboard. A
  site's need becomes a conf key with the value in the site's `citrine/`.
- **The ISO is one file for everyone.** Nothing about a site or a machine goes on
  it: that's what setup is for.
- **No credential in the image, this repository or a site's.** They reach a machine
  through setup or `secrets`, stay in `/etc/citrine/secrets.env`, and leave it only
  through its pool record and its backups.
- **Only a site's `BUILD` is built on the server**, rootless and as the user.
  Every other container is a registry image —
  [.agents/docs/citrine.md](.agents/docs/citrine.md).
- **Nothing from an app's image runs as root.** `citrine-app` takes its files
  with `podman cp` and parses `app.conf`: never `podman run` it, and never source
  a file out of it.
- **No Containerfile uses a heredoc** —
  [.agents/docs/citrine.md](.agents/docs/citrine.md).
- **Latest everything.** No fallbacks for older podman, systemd or browsers
  beyond what the docs name.
