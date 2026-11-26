package main

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

type containersTab struct {
	sel, off int
}

var (
	kLogs     = bind("enter", "logs", "enter")
	kRestart  = bind("r", "restart", "r")
	kStop     = bind("s", "stop/start", "s")
	kPull     = bind("p", "pull & restart", "p")
	kBuild    = bind("p", "build & restart", "p")
	kRollback = bind("b", "roll back", "b")
	kUpdate   = bind("u", "update all", "u")
	kCheck    = bind("c", "check", "c")
	kInstall  = bind("i", "install app", "i")
)

func (containersTab) title() string { return "Containers" }

func (containersTab) needs() []kind {
	return []kind{kContainers, kUserUnits, kStats, kImages, kPending}
}

func (c *containersTab) keys(a *app) []key.Binding {
	out := []key.Binding{kLogs, kRestart, kStop, kPull}
	ct, ok := c.current(a)
	if ok && c.built(a, ct) != "" {
		out[3] = kBuild
	}
	if ok && c.ours(a, ct) && hasTag(a.s.images.val, c.previous(a, ct)) {
		out = append(out, kRollback)
	}
	return append(out, kUpdate, kCheck, kInstall)
}

func (c *containersTab) current(a *app) (container, bool) {
	cs := a.containers()
	if len(cs) == 0 {
		return container{}, false
	}
	c.sel = clamp(c.sel, 0, len(cs)-1)
	return cs[c.sel], true
}

func (containersTab) app(ct container) string { return strings.TrimSuffix(ct.q.unit, ".service") }

func (containersTab) built(a *app, ct container) string {
	name, ok := strings.CutPrefix(ct.q.image, "localhost/")
	if !ok {
		return ""
	}
	name = strings.TrimSuffix(name, ":latest")
	for _, pair := range strings.Fields(a.cfg["BUILD"]) {
		if n, _, _ := strings.Cut(pair, "="); n == name {
			return name
		}
	}
	return ""
}

func (c containersTab) ours(a *app, ct container) bool {
	reg := a.cfg["REGISTRY"]
	return c.built(a, ct) != "" || reg != "" && ct.q.image == reg+"/"+c.app(ct)+":latest"
}

func (c containersTab) previous(a *app, ct container) string {
	if n := c.built(a, ct); n != "" {
		return "localhost/" + n + ":previous"
	}
	return a.cfg["REGISTRY"] + "/" + c.app(ct) + ":previous"
}

func (containersTab) selects(msg tea.KeyPressMsg) bool {
	return key.Matches(msg, kSelect, kTop, kBottom)
}

func (c *containersTab) key(a *app, msg tea.KeyPressMsg) tea.Cmd {
	n := len(a.containers())
	switch {
	case key.Matches(msg, kUp):
		c.sel = clamp(c.sel-1, 0, n-1)
		return nil
	case key.Matches(msg, kDown):
		c.sel = clamp(c.sel+1, 0, n-1)
		return nil
	case key.Matches(msg, kTop):
		c.sel = 0
		return nil
	case key.Matches(msg, kBottom):
		c.sel = max(0, n-1)
		return nil
	case key.Matches(msg, kUpdate):
		return a.confirm("Update every container?",
			"Pulls each image marked AutoUpdate=registry and restarts the ones that changed. The rest are left alone.",
			"Update", false, startCmd(spec{
				key: "auto-update", title: "Update containers", user: true,
				argv: systemctl(true, "start", "podman-auto-update.service"), unit: "podman-auto-update.service",
				done: "Containers updated", after: []kind{kContainers, kImages, kPending},
			}))
	case key.Matches(msg, kInstall):
		return c.askApp(a)
	case key.Matches(msg, kCheck):
		a.notify(lvInfo, "asking the registries what's new")
		a.onLoad(kPending, func() tea.Cmd {
			c.checked(a)
			return nil
		})
		return a.reload(kPending)
	}
	ct, ok := c.current(a)
	if !ok {
		return nil
	}
	name, unitName := ct.q.name, ct.q.unit
	switch {
	case key.Matches(msg, kLogs):
		return a.showLogs(source{label: name, unit: unitName, user: true})
	case key.Matches(msg, kRestart):
		return a.confirm("Restart "+name+"?", c.impact(ct), "Restart", false, startCmd(spec{
			key: "restart:" + unitName, title: "Restart " + name, user: true,
			argv: systemctl(true, "restart", unitName), unit: unitName,
			done: name + " restarted", after: []kind{kContainers, kStats},
		}))
	case key.Matches(msg, kStop):
		if ct.running || ct.unit.active == "active" || ct.unit.active == "activating" {
			return a.confirm("Stop "+name+"?", c.impact(ct)+" It stays down until you start it or the machine reboots.", "Stop", true, startCmd(spec{
				key: "stop:" + unitName, title: "Stop " + name, user: true,
				argv: systemctl(true, "stop", unitName), unit: unitName,
				done: name + " stopped", after: []kind{kContainers, kStats},
			}))
		}
		return a.start(spec{
			key: "start:" + unitName, title: "Start " + name, user: true,
			argv: systemctl(true, "start", unitName), unit: unitName,
			done: name + " started", after: []kind{kContainers, kStats},
		})
	case key.Matches(msg, kPull):
		return c.pull(a, ct)
	case key.Matches(msg, kRollback):
		prev := c.previous(a, ct)
		if !c.ours(a, ct) || !hasTag(a.s.images.val, prev) {
			return nil
		}
		latest := strings.TrimSuffix(prev, ":previous") + ":latest"
		argv, unit := transient("rollback-"+c.app(ct), "Roll back "+name, true,
			"/bin/sh", "-c", fmt.Sprintf("podman tag %s %s && systemctl --user restart %s", prev, latest, unitName))
		return a.confirm("Roll "+name+" back?", "Points :latest at the image the last deploy or build replaced and restarts it. The next deploy, build or auto-update moves it forward again.", "Roll back", true, startCmd(spec{
			key: "rollback:" + unitName, title: "Roll back " + name, user: true, argv: argv, unit: unit,
			done: name + " rolled back", after: []kind{kContainers, kImages},
		}))
	}
	return nil
}

type appPreview struct {
	args []string
	text string
	err  error
}

func (containersTab) askApp(a *app) tea.Cmd {
	line := ""
	field := huh.NewInput().Title("Install an app").
		Description("An image carrying a Citrine app, then any of --join <containers> and --env <files>.").
		Placeholder("ghcr.io/you/app:latest --join web,db").Value(&line)
	return a.open(huh.NewGroup(field), false, func() tea.Cmd {
		args := strings.Fields(line)
		if len(args) == 0 {
			return nil
		}
		a.notify(lvInfo, "reading "+args[0])
		return func() tea.Msg {
			out, err := run(5*time.Minute, "citrine-app", "show", args[0])
			text, _, _ := strings.Cut(string(out), "\n\n")
			shown := strings.Split(text, "\n")
			for i, l := range shown {
				shown[i] = clean(l)
			}
			return appPreview{args: args, text: strings.Join(shown, "\n"), err: err}
		}
	})
}

func (containersTab) confirmApp(a *app, p appPreview) tea.Cmd {
	if p.err != nil {
		a.notify(lvBad, "couldn't read "+p.args[0]+": "+errText(p.err))
		return nil
	}
	argv, unit := transient("app-install", "Install an app", false,
		append(append([]string{"/usr/bin/citrine-app", "install"}, p.args...), "--yes")...)
	return a.confirm("Install this app?", p.text+"\n\nIt can read whatever this user can.", "Install", true, startCmd(spec{
		key: "app-install", title: "Install " + p.args[0], root: true, argv: argv, unit: unit,
		done: "App installed", after: []kind{kContainers},
	}))
}

func (containersTab) impact(ct container) string {
	if ct.q.impact != "" {
		return ct.q.impact
	}
	return "It's briefly unavailable."
}

func (c containersTab) checked(a *app) {
	if err := a.s.pending.err; err != nil {
		a.notify(lvBad, "couldn't check: "+errText(err))
		return
	}
	var waiting []string
	for _, ct := range a.containers() {
		if ct.pending == "pending" {
			waiting = append(waiting, ct.q.name)
		}
	}
	if len(waiting) == 0 {
		a.notify(lvOK, "everything's current")
		return
	}
	a.notify(lvInfo, plural(len(waiting), "update", "updates")+" waiting: "+strings.Join(waiting, ", "))
}

func (c containersTab) pull(a *app, ct container) tea.Cmd {
	name, unitName := ct.q.name, ct.q.unit
	if n := c.built(a, ct); n != "" {
		return a.confirm("Build "+name+"?", "Fetches the site and builds "+n+" if its folder changed since the image "+name+" runs, then restarts it on the new one. "+c.impact(ct), "Build", false, startCmd(spec{
			key: "start:citrine-site.service", title: "Sync the site", root: true,
			argv: systemctl(false, "start", "citrine-site.service"), unit: "citrine-site.service",
			done: "Site synced", after: []kind{kContainers, kImages},
		}))
	}
	if c.ours(a, ct) {
		argv, unit := transient("deploy-"+c.app(ct), "Deploy "+name, true, "/usr/bin/deploy", c.app(ct))
		return a.confirm("Deploy "+name+"?", "Pulls "+shortImage(ct.q.image)+" and restarts it, keeping the old image as :previous. "+c.impact(ct), "Deploy", false, startCmd(spec{
			key: "pull:" + unitName, title: "Deploy " + name, user: true, argv: argv, unit: unit,
			done: name + " deployed", after: []kind{kContainers, kImages, kPending},
		}))
	}
	body := "Pulls " + ct.q.image + " and restarts it. " + c.impact(ct)
	argv, unit := transient("pull-"+c.app(ct), "Pull "+name, true,
		"/bin/sh", "-c", fmt.Sprintf("podman pull %s && systemctl --user restart %s", ct.q.image, unitName))
	return a.confirm("Pull "+name+"?", body, "Pull", false, startCmd(spec{
		key: "pull:" + unitName, title: "Pull " + name, user: true, argv: argv, unit: unit,
		done: name + " pulled", after: []kind{kContainers, kImages, kPending},
	}))
}

func startCmd(s spec) tea.Cmd {
	return func() tea.Msg { return startMsg{s} }
}

func (c *containersTab) view(a *app, w, h int) string {
	t := a.t
	r := a.s.pods
	if !r.ok {
		if r.err != nil {
			return t.tint(lvBad, "Can't read the containers: ") + t.muted(errText(r.err))
		}
		return t.dim(a.spin.View() + " reading the containers")
	}
	cs := a.containers()
	if len(cs) == 0 {
		return t.muted("No quadlets in " + a.cfg.quadlets())
	}
	c.sel = clamp(c.sel, 0, len(cs)-1)
	cols := []col{{title: "container", w: 24}, {title: "state", w: 12}, {title: "up", w: 7}, {title: "cpu", w: 6, right: true}, {title: "memory", w: 8, right: true}, {title: "image", w: 0}}
	switch {
	case w < 64:
		cols = []col{{title: "container", w: 0}, {title: "state", w: 12}, {title: "memory", w: 8, right: true}}
	case w < 84:
		cols = []col{{title: "container", w: 0}, {title: "state", w: 12}, {title: "up", w: 7}, {title: "cpu", w: 6, right: true}, {title: "memory", w: 8, right: true}}
	}
	rows := make([][]string, len(cs))
	for i, ct := range cs {
		lv, state := ct.state()
		name := t.soft(ct.q.name)
		if i == c.sel {
			name = t.bold(ct.q.name)
		}
		cpu, mem := t.dim("—"), t.dim("—")
		if ct.hasStat {
			cpu, mem = t.muted(fmt.Sprintf("%.1f%%", ct.stats.cpu*100)), t.muted(size(ct.stats.mem))
		}
		up := t.dim("—")
		if d := ct.up(); d > 0 {
			up = t.muted(span(d))
		}
		img := t.faint(shortImage(ct.q.image))
		if !ct.image.created.IsZero() {
			img += t.dim(" · " + span(time.Since(ct.image.created)))
		}
		if ct.pending == "pending" {
			img = t.tint(lvInfo, "↑ ") + img
		}
		state = t.dot(lv) + " " + t.tint(lv, state)
		rows[i] = []string{name, state, up, cpu, mem, img}
		switch len(cols) {
		case 3:
			rows[i] = []string{name, state, mem}
		case 5:
			rows[i] = rows[i][:5]
		}
	}
	tableH := min(len(rows)+1, max(4, h/2))
	detail := c.detail(a, cs[c.sel], w-4)
	room := h - tableH - 3
	if room < len(detail) {
		detail = detail[:max(0, room)]
	}
	tableH = h
	if len(detail) > 0 {
		tableH = h - len(detail) - 3
	}
	table := strings.Join(t.table(cols, rows, c.sel, w, min(len(rows)+1, tableH), &c.off), "\n")
	if len(detail) == 0 {
		return table
	}
	return table + "\n\n" + t.box(t.accent(cs[c.sel].q.name), detail, w, 0)
}

func (c containersTab) detail(a *app, ct container, iw int) []string {
	t := a.t
	kw := 10
	var lines []string
	img := t.muted(ct.q.image)
	if !ct.image.created.IsZero() {
		img += t.dim(" · built " + ago(ct.image.created))
	}
	if ct.image.digest != "" {
		img += t.dim(" · " + shortDigest(ct.image.digest))
	}
	switch ct.pending {
	case "pending":
		img += "  " + t.tag(lvInfo, "update waiting")
	case "false":
		img += "  " + t.dim("current")
	}
	lines = append(lines, t.wrapKV("image", img, kw, iw)...)
	var net []string
	if len(ct.q.networks) > 0 {
		net = append(net, strings.Join(ct.q.networks, ", "))
	}
	if ct.q.autoUpdate != "" {
		net = append(net, t.dim("auto-update ")+ct.q.autoUpdate)
	} else {
		net = append(net, t.dim("updated by hand"))
	}
	u := ct.unit
	unitText := t.muted(ct.q.unit + " " + u.active)
	if u.sub != "" {
		unitText += t.dim(" (" + u.sub + ")")
	}
	if !u.since.IsZero() {
		unitText += t.dim(" since " + ago(u.since))
	}
	if u.restarts > 0 {
		unitText += t.tint(lvWarn, fmt.Sprintf(" · restarted %d×", u.restarts))
	}
	lines = append(lines, t.wrapKV("unit", unitText, kw, iw)...)
	if ct.hasStat && ct.stats.pids > 0 {
		lines = append(lines, t.keyval("load", t.muted(fmt.Sprintf("%.1f%% cpu · %s · %d processes", ct.stats.cpu*100, size(ct.stats.mem), ct.stats.pids)), kw))
	}
	if len(ct.q.ports) > 0 {
		lines = append(lines, t.wrapKV("ports", t.muted(strings.Join(ct.q.ports, ", ")), kw, iw)...)
	}
	lines = append(lines, t.wrapKV("networks", t.muted(strings.Join(net, t.dim(" · "))), kw, iw)...)
	if ct.q.description != "" {
		lines = append(lines, t.wrapKV("about", t.soft(ct.q.description), kw, iw)...)
	}
	return lines
}
