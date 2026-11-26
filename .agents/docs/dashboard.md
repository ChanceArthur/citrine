# The dashboard

`dashboard/` is the login session: logging in as `USER_NAME`, over SSH or at the
machine, lands in it.

## Getting in and out

- **`profile.d/dashboard.sh` starts it only for an interactive login shell on a
  terminal**, and `dashboard --login` steps aside for anyone but `USER_UID`.
  Fedora's `/etc/bashrc` sources `profile.d` for non-login shells too, which is
  what the login-shell test is for; `CITRINE_DASHBOARD` guards the shells the
  dashboard opens itself.
- `dashboard --login && exit`: a clean quit ends the session, a crash falls back
  to the shell.
- **Reboot and power off quit the dashboard before asking systemd** (`spec.last`):
  the shutdown drops the connection before the dashboard can leave the alt screen
  and turn mouse reporting off, and the terminal back home then types every
  click into its shell as escape codes, beeping. `ssh -t <host> bash` gets a plain shell — `bash -l` is a
  login shell, and gets the dashboard.

## Doing things

- **Anything that changes the machine runs as a systemd unit** — an existing
  one through `systemctl start`, anything else through `systemd-run --collect`
  — and the dashboard follows that unit's journal. The command gets its own
  session only when sudo needs no password: sudo keys a typed password to the
  terminal, and a command in another session finds it locked.
- **The user is in `wheel`, which sudo asks for a password** — FCOS's
  passwordless rule is for the `sudo` group. The dashboard asks for it in a
  dialog and hands it to `sudo -S -v`.
- **`/etc/sudoers.d/citrine` lets the user run `look` without a password**, so
  `look` stays read-only and checks every argument. That rule also makes
  `sudo -n -l` succeed with nothing cached, which is why an action checks with
  `sudo -n -v` first.
- **`podman auto-update --dry-run` only sees containers marked `AutoUpdate`**,
  so the check asks the registry itself for the rest: the digest of
  `skopeo inspect --raw`'s bytes, or this platform's entry in a multi-arch
  list, against the local image's `RepoDigests`.
- **systemd forgets a service's exit time and result at every boot**, so a
  service that hasn't run since takes its last run from its manager's own
  succeeded/finished/failed messages in the journal. A timer's last trigger survives
  only because the timers are `Persistent=true`.
- **A user manager logs "Deactivated successfully" at debug**, so a user unit's
  journal holds only its failures: its success is the start job's "Finished"
  (`UNIT_STARTED`). Without it, every boot shows a user timer's last failure,
  however many runs ago.
- **The containers come from the quadlet files, not `podman ps`**: quadlet runs
  them with `--rm`, so a failed one is gone from `podman ps -a`.
- **A secret reaches `secrets` on stdin, never on a command line**, which `spec.stdin`
  is for: an `argv` is in `/proc`, and a transient unit's in the journal. So it runs
  directly rather than through `systemd-run`.
- **The dashboard runs `usb-adopt … force`, `restore-db` and `look` itself**, so
  changing a script's arguments, prompt or output is changing the dashboard too.
- `restore-db` reads its own "Type yes" from stdin, and the transient unit
  answers it with `StandardInputText` — after the dashboard's typed confirmation.
- **Podman files everything a container writes to stderr at priority 3**, and
  cloudflared and Postgres write their routine lines there. A container's line
  takes the level it states, so the error views filter after reading rather
  than trusting `journalctl -p`.
- **Tailscale's SELinux health warning is left out of the Network tab**: it
  fires whenever SELinux enforces, which on FCOS is always, and Tailscale SSH
  works here regardless.
- Child commands run under `LC_ALL=C.UTF-8`: `btrfs scrub status` prints its
  dates in the locale.

## Cloudflare

- **The token comes from `/etc/citrine/env/dashboard.env`**, which
  `site apply` writes: `secrets.env` is root's alone, and the env files are
  readable by the user's group.
- **The Free plan keeps 31 days and answers at most 30 per query**, so a
  window longer than 30d makes the Cloudflare tab fail outright.
- **A rule's name exists only on raw `firewallEventsAdaptive` events**; the
  grouped dataset is off on Free. The Security chart groups by source and
  action instead, and only the event list names rules.
- **`link_maze_injected` is not a stop**: it's the Labyrinth hiding its link
  in a page that was served. Only `link_maze_visited` is a bot caught.
- **`--setup` runs only the setup screen**, and every entry point lands there while
  setup is pending. `setup.go` is its own small program, so nothing in the main app
  runs before the machine has a password that isn't public.
- **The Allowed tab's visitors are the shield Worker's own log lines, tailed into
  `citrine-visits`' journal by `dashboard --visits`**: Free has no raw HTTP
  requests, keeps Workers Logs 3 days, and has no Tail Workers or Logpush, so
  a held tail is the only way to keep 30 days.
- **A tail stops delivering at its `expires_at` without closing the socket**, so
  `--visits` opens the next one before then and drops the old after; the
  overlap is why it skips a `cf-ray` it has already written. Cloudflare allows
  10 tails per Worker, a dashboard's and `wrangler tail`'s included, and a
  busy Worker's tail samples.
- **A dead path shows on a tail's socket only when TCP keepalive gives up,
  minutes later**, and every visit in between is lost. The WebSocket ping is
  what catches it in seconds; Cloudflare answers it. Pinging doesn't keep the
  path up: on the owner's network over IPv4, a pinged tail and an unpinged one
  died to the same drop, every few minutes.
- **The Allowed tab warns when Cloudflare served a request more than 10 minutes
  after the newest logged visit**, which is how a dead tail shows. It counts
  only requests whose `cacheStatus` isn't `none`: what Cloudflare answers
  itself — `/cdn-cgi/`, edge redirects, a managed `robots.txt` — never reaches
  the Worker, and those once raised it all day. The Worker-less `_astro/*`
  routes still count, so assets fetched alone can raise it.
- **The stops are a query of their own**: one field GraphQL doesn't know fails
  every alias in the request, and theirs are the fields least documented.
- **The shield Worker's fetch to the origin is a second request** in
  `httpRequestsAdaptiveGroups`, so every breakdown filters to `eyeball` —
  except origin timing, which only those fetches carry. The overview dataset
  can't filter on `requestSource` and doesn't need to.
- **The shield Worker's 403 is no firewall event.** The dashboard takes an eyeball 403
  with no security source to be one, which holds only while the site itself
  never answers 403. Free hides `clientAsn` and the query string there, so its
  stops carry neither, nor a ray.

## Drawing

- **`Canvas.Compose(layer)` ignores the layer's X and Y and clears the whole
  canvas.** Anything positioned goes through a `Compositor`.
- Huh sizes a group's viewport at 80 columns when it's built, so a narrower
  dialog hides its buttons until the form sees a `WindowSizeMsg` — `open`
  sends it one.
- **Bubbles' `ShortHelpView` overruns its width when the item that doesn't fit
  leaves no room for its `…`**, and `spread` then drops the whole key guide.
  `shortHelp` narrows the width until the result fits.
- **A tab that moves a selection with ↑↓ implements `selector`.** Once a
  tab is taller than the window, the app's scroller takes every arrow the tab
  doesn't claim.
- `TERM=linux` is the machine's own screen, whose font has no heavy lines,
  eighth blocks or rounded corners; `theme.plain` draws with what it has.
- **The dashboard's colours are its own and don't follow any site's.**
