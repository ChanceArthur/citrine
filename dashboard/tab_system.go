package main

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

type systemTab struct{}

var (
	kOSUpdate  = bind("u", "update", "u")
	kOSCheck   = bind("c", "check", "c")
	kRebase    = bind("v", "version", "v")
	kOSBack    = bind("b", "roll back", "b")
	kReboot    = bind("R", "reboot", "R")
	kPowerOff  = bind("P", "power off", "P")
	kFirstBoot = bind("f", "first boot", "f")
	kSecrets   = bind("s", "secrets", "s")
)

func (systemTab) title() string { return "System" }

func (systemTab) needs() []kind { return []kind{kOstree, kUnits} }

func (systemTab) keys(*app) []key.Binding {
	return []key.Binding{kOSUpdate, kOSCheck, kRebase, kOSBack, kReboot, kPowerOff, kFirstBoot, kSecrets}
}

func (s systemTab) key(a *app, msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, kSecrets):
		return func() tea.Msg {
			out, err := run(20*time.Second, look("secrets")...)
			return secretsListed{groups: parseSecrets(out), err: err}
		}
	case key.Matches(msg, kOSUpdate):
		argv, unit := transient("os-update", "Update Citrine", false, "rpm-ostree", "upgrade")
		return a.confirm("Update Citrine now?",
			"Pulls the newest image and stages it. Nothing changes until a reboot — the reboot window takes it on "+clock(a.unit("citrine-reboot.timer").next)+", or R does it now.",
			"Update", false, startCmd(spec{
				key: "os", title: "Update Citrine", root: true, argv: argv, unit: unit,
				done: "Citrine staged", after: []kind{kOstree},
			}))
	case key.Matches(msg, kOSCheck):
		argv, unit := transient("os-check", "Check for a new Citrine", false, "/bin/sh", "-c", "rpm-ostree upgrade --check || [ $? -eq 77 ]")
		return a.start(spec{
			key: "os", title: "Check for a new Citrine", root: true, argv: argv, unit: unit,
			done: "Checked for a new Citrine", after: []kind{kOstree},
		})
	case key.Matches(msg, kRebase):
		if a.s.versions.ok {
			return s.rebase(a)
		}
		a.notify(lvInfo, "asking GHCR for versions")
		a.onLoad(kVersions, func() tea.Cmd { return s.rebase(a) })
		return a.reload(kVersions)
	case key.Matches(msg, kOSBack):
		if _, ok := a.s.ostree.val.rollback(); !ok {
			a.notify(lvWarn, "there's no other deployment to roll back to")
			return nil
		}
		argv, unit := transient("os-rollback", "Roll Citrine back", false, "rpm-ostree", "rollback")
		return a.confirm("Roll back to the previous deployment?",
			"Makes the previous Citrine the default for the next boot. Reboot to use it — and the next update moves forward again unless you pin a version.",
			"Roll back", true, startCmd(spec{
				key: "os", title: "Roll Citrine back", root: true, argv: argv, unit: unit,
				done: "Rolled back — reboot to use it", after: []kind{kOstree},
			}))
	case key.Matches(msg, kReboot):
		body := "The site is down until the machine is back, usually a minute or two. This session ends."
		if d, ok := a.s.ostree.val.staged(); ok {
			body = "Boots into " + d.name() + ". " + body
		}
		return a.confirm("Reboot "+a.host+"?", body, "Reboot", true, startCmd(spec{
			key: "power", title: "Reboot", root: true, argv: systemctl(false, "reboot"), last: true,
		}))
	case key.Matches(msg, kPowerOff):
		return a.typed("Power off "+a.host+"?",
			"Nothing here can turn it back on — someone has to press the button. The site is down until then. Type off to go ahead.",
			"off", startCmd(spec{key: "power", title: "Power off", root: true, argv: systemctl(false, "poweroff"), last: true}))
	case key.Matches(msg, kFirstBoot):
		return a.confirm("Run first boot again?",
			"Rejoins the tailnet, rewrites the share password, the registry login and tailscale serve, and starts the containers. Safe to repeat; it's how to finish after setting TS_AUTHKEY by hand.",
			"Run it", false, startCmd(spec{
				key: "first-boot", title: "First boot", root: true,
				argv: []string{"/bin/sh", "-c", "rm -f " + firstBootTag + " && systemctl restart citrine-first-boot.service"},
				unit: "citrine-first-boot.service", done: "First boot finished", after: []kind{kTailnet, kContainers},
			}))
	}
	return nil
}

func (s systemTab) rebase(a *app) tea.Cmd {
	booted, _ := a.s.ostree.val.booted()
	current := booted.tag()
	opts := []huh.Option[string]{huh.NewOption(mark("latest — follow every build", current == "latest"), "latest")}
	for i, v := range a.s.versions.val {
		if i >= 30 {
			break
		}
		label := v
		if booted.citrine.version == v {
			label += "  (running)"
		}
		opts = append(opts, huh.NewOption(mark(label, current == v), v))
	}
	return a.choose("Move to which Citrine?", "A version pins the machine there — nothing updates until you choose latest again.", opts, func(tag string) tea.Cmd {
		if tag == "" {
			return nil
		}
		ref := "ostree-image-signed:docker://" + a.cfg.repo() + ":" + tag
		argv, unit := transient("os-rebase", "Rebase Citrine", false, "rpm-ostree", "rebase", ref)
		body := "Stages " + tag + " for the next boot."
		if tag != "latest" {
			body += " The machine stays on it until you choose latest again."
		}
		return a.confirm("Rebase onto "+tag+"?", body, "Rebase", tag != "latest", startCmd(spec{
			key: "os", title: "Rebase onto " + tag, root: true, argv: argv, unit: unit,
			done: "Staged " + tag + " — reboot to use it", after: []kind{kOstree},
		}))
	})
}

type secretsListed struct {
	groups []secretGroup
	err    error
}

func saveSecrets(block string) tea.Cmd {
	return startCmd(spec{
		key: "secrets", title: "Save secrets", root: true, stdin: block,
		argv: []string{libexec + "/secrets", "set"},
		done: "Secrets saved and applied", after: []kind{kContainers},
	})
}

func (systemTab) chooseSecret(a *app, l secretsListed) tea.Cmd {
	if l.err != nil {
		a.notify(lvBad, "couldn't read the secrets: "+errText(l.err))
		return nil
	}
	const paste = "paste"
	opts := []huh.Option[string]{huh.NewOption("Paste a block of them", paste)}
	notes := map[string]string{}
	set := map[string]bool{}
	for _, g := range l.groups {
		for _, k := range g.keys {
			opts = append(opts, huh.NewOption(mark(k.name, k.set), k.name))
			notes[k.name], set[k.name] = g.note, k.set
		}
	}
	return a.choose("Which secret?", "Saving one applies it, and restarts the containers that read it. ✓ means it's set.", opts, func(name string) tea.Cmd {
		switch {
		case name == "":
			return nil
		case name == paste:
			block := ""
			return a.open(huh.NewGroup(secretPaste(&block)), false, func() tea.Cmd { return saveSecrets(block) })
		case !set[name]:
			return systemTab{}.enterSecret(a, name, notes[name])
		}
		return a.choose(name, "It's set, and nothing shows what to.", []huh.Option[string]{
			huh.NewOption("Replace it", "replace"),
			huh.NewOption("Remove it", "remove"),
		}, func(how string) tea.Cmd {
			switch how {
			case "replace":
				return systemTab{}.enterSecret(a, name, notes[name])
			case "remove":
				return a.confirm("Remove "+name+"?", "Whatever reads it restarts without it.", "Remove", true, startCmd(spec{
					key: "secrets", title: "Remove " + name, root: true,
					argv: []string{libexec + "/secrets", "unset", name},
					done: "Removed " + name, after: []kind{kContainers},
				}))
			}
			return nil
		})
	})
}

func (systemTab) enterSecret(a *app, name, note string) tea.Cmd {
	val := ""
	field := huh.NewInput().Title(name).Description(note).EchoMode(huh.EchoModePassword).Value(&val)
	return a.open(huh.NewGroup(field), false, func() tea.Cmd {
		if v := strings.TrimSpace(val); v != "" {
			return saveSecrets(name + "=" + v + "\n")
		}
		return nil
	})
}

func mark(label string, on bool) string {
	if on {
		return label + "  ✓"
	}
	return label
}

func (s systemTab) view(a *app, w, h int) string {
	t := a.t
	kw := 10
	info := []string{t.keyval("hostname", t.text(orDash(a.host)), kw)}
	ver := t.text(orDash(a.rel.version))
	if c := a.rel.short(); c != "" {
		ver += t.dim(" · commit ") + t.muted(c)
	}
	info = append(info, t.keyval("version", ver, kw))
	r := a.s.ostree
	booted, hasBooted := r.val.booted()
	if hasBooted {
		ref := booted.image()
		follows := t.dim(" — follows every build")
		if tag := booted.tag(); tag != "" && tag != "latest" {
			follows = t.tint(lvWarn, " — pinned, nothing updates")
		}
		info = append(info, t.keyval("image", t.muted(ref)+follows, kw))
	}
	k := t.muted(kernel())
	if a.s.sys.uptime > 0 {
		k += t.dim(" · up ") + t.muted(span(a.s.sys.uptime))
	}
	info = append(info, t.keyval("kernel", k, kw))
	upd := a.unit("rpm-ostreed-automatic.timer")
	reboot := a.unit("citrine-reboot.timer")
	info = append(info, t.keyval("updates", t.muted("checks ")+t.accent(clock(upd.next))+t.dim(", reboots as soon as one stages"), kw))
	info = append(info, t.keyval("", t.dim("reboot window ")+t.accent(clock(reboot.next))+t.dim(" for anything staged by hand"), kw))
	out := t.panel(t.bold("Citrine"), strings.Join(info, "\n"), w)

	out += "\n\n" + t.heading("Deployments") + "\n"
	switch {
	case !r.ok && r.err != nil:
		return out + t.muted(errText(r.err))
	case !r.ok:
		return out + t.dim(a.spin.View()+" asking rpm-ostree")
	}
	if r.val.busy {
		out += t.tint(lvInfo, a.spin.View()+" rpm-ostree is working on something") + "\n"
	}
	cols := []col{{title: "", w: 10}, {title: "version", w: 12}, {title: "built", w: 10}, {title: "image", w: 0}}
	var rows [][]string
	rollback, hasRollback := r.val.rollback()
	for _, d := range r.val.deployments {
		role, lv := "", lvNone
		switch {
		case d.booted:
			role, lv = "booted", lvOK
		case d.staged:
			role, lv = "staged", lvInfo
		case hasRollback && d.checksum == rollback.checksum:
			role = "rollback"
		}
		img := t.faint(":" + orDash(d.tag()))
		if d.digest != "" {
			img += t.dim("  " + shortDigest(d.digest))
		}
		if d.staged {
			img += t.tint(lvInfo, "  → reboots "+clock(reboot.next))
		}
		rows = append(rows, []string{t.dot(lv) + " " + t.tint(lv, role), t.text(d.name()), t.muted(span(time.Since(d.timestamp)) + " ago"), img})
	}
	out += strings.Join(t.table(cols, rows, -1, w, len(rows)+1, nil), "\n")
	if r.val.update != "" {
		out += "\n\n" + t.tint(lvInfo, "↑ "+r.val.update+" is waiting in the registry") + t.dim(" — u stages it")
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
