package main

import (
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

type svcRow struct {
	header string
	u      unit
	timer  bool
	failed bool
}

type servicesTab struct {
	sel, off int
}

var (
	kRunNow   = bind("s", "start", "s")
	kStopUnit = bind("S", "stop", "S")
)

func (servicesTab) title() string { return "Services" }

func (servicesTab) needs() []kind { return []kind{kUnits, kUserUnits, kFailed, kContainers} }

func (servicesTab) keys(*app) []key.Binding {
	return []key.Binding{kLogs, kRunNow, kRestart, kStopUnit}
}

func (servicesTab) rows(a *app) []svcRow {
	var timers, services []unit
	for _, u := range a.s.units.val.units {
		if u.load == "not-found" {
			continue
		}
		if strings.HasSuffix(u.id, ".timer") {
			timers = append(timers, u)
		} else {
			services = append(services, u)
		}
	}
	for _, name := range userExtras {
		u, ok := a.s.userUnits.val.units[name]
		if !ok || u.load == "not-found" {
			continue
		}
		if strings.HasSuffix(u.id, ".timer") {
			timers = append(timers, u)
		} else {
			services = append(services, u)
		}
	}
	sort.Slice(timers, func(i, j int) bool {
		if timers[i].next.IsZero() != timers[j].next.IsZero() {
			return !timers[i].next.IsZero()
		}
		if !timers[i].next.Equal(timers[j].next) {
			return timers[i].next.Before(timers[j].next)
		}
		return timers[i].id < timers[j].id
	})
	sort.Slice(services, func(i, j int) bool {
		ci, cj := strings.HasPrefix(services[i].id, "citrine-"), strings.HasPrefix(services[j].id, "citrine-")
		if ci != cj {
			return ci
		}
		if services[i].user != services[j].user {
			return !services[i].user
		}
		return services[i].id < services[j].id
	})
	var out []svcRow
	if len(timers) > 0 {
		out = append(out, svcRow{header: "Scheduled"})
		for _, u := range timers {
			out = append(out, svcRow{u: u, timer: true})
		}
	}
	if len(services) > 0 {
		out = append(out, svcRow{header: "Services"})
		for _, u := range services {
			out = append(out, svcRow{u: u})
		}
	}
	if f := a.s.failed.val; len(f) > 0 {
		out = append(out, svcRow{header: "Failed"})
		for _, fu := range f {
			u := a.unit(fu.name)
			if u.id == "" {
				u = unit{id: fu.name, description: fu.description, active: "failed", user: fu.user}
			}
			out = append(out, svcRow{u: u, failed: true})
		}
	}
	return out
}

func (s *servicesTab) current(a *app) (svcRow, bool) {
	rows := s.rows(a)
	if len(rows) == 0 {
		return svcRow{}, false
	}
	s.sel = s.settle(rows, clamp(s.sel, 0, len(rows)-1), 1)
	if rows[s.sel].header != "" {
		return svcRow{}, false
	}
	return rows[s.sel], true
}

func (servicesTab) settle(rows []svcRow, i, dir int) int {
	for j := i; j >= 0 && j < len(rows); j += dir {
		if rows[j].header == "" {
			return j
		}
	}
	for j := i; j >= 0 && j < len(rows); j -= dir {
		if rows[j].header == "" {
			return j
		}
	}
	return i
}

func (r svcRow) service(a *app) (string, bool) {
	if r.timer && r.u.triggers != "" {
		return r.u.triggers, r.u.user
	}
	return r.u.id, r.u.user
}

func (servicesTab) selects(msg tea.KeyPressMsg) bool { return key.Matches(msg, kSelect, kTop, kBottom) }

func (s *servicesTab) key(a *app, msg tea.KeyPressMsg) tea.Cmd {
	rows := s.rows(a)
	switch {
	case key.Matches(msg, kUp):
		s.sel = s.settle(rows, clamp(s.sel-1, 0, len(rows)-1), -1)
		return nil
	case key.Matches(msg, kDown):
		s.sel = s.settle(rows, clamp(s.sel+1, 0, len(rows)-1), 1)
		return nil
	case key.Matches(msg, kTop):
		s.sel = s.settle(rows, 0, 1)
		return nil
	case key.Matches(msg, kBottom):
		s.sel = s.settle(rows, len(rows)-1, -1)
		return nil
	}
	row, ok := s.current(a)
	if !ok {
		return nil
	}
	name, user := row.service(a)
	label := baseName(name)
	switch {
	case key.Matches(msg, kLogs):
		return a.showLogs(source{label: label, unit: name, user: user})
	case key.Matches(msg, kRunNow):
		return a.confirm("Start "+label+" now?", s.warning(name, "start"), "Start", name == "citrine-reboot.service", startCmd(s.action(name, user, "start", "Start "+label)))
	case key.Matches(msg, kRestart):
		return a.confirm("Restart "+label+"?", s.warning(name, "restart"), "Restart", true, startCmd(s.action(name, user, "restart", "Restart "+label)))
	case key.Matches(msg, kStopUnit):
		return a.confirm("Stop "+label+"?", s.warning(name, "stop"), "Stop", true, startCmd(s.action(name, user, "stop", "Stop "+label)))
	}
	return nil
}

func (servicesTab) action(name string, user bool, verb, title string) spec {
	return spec{
		key: verb + ":" + name, title: title, user: user, root: !user,
		argv: systemctl(user, verb, name), unit: name,
		after: []kind{kUnits, kUserUnits, kFailed},
	}
}

func (servicesTab) warning(name, verb string) string {
	switch {
	case name == "tailscaled.service" && verb != "start":
		return "Tailscale SSH rides on it — this session will probably drop, and the tailnet URLs go dark until it's back."
	case name == "sshd.service" && verb != "start":
		return "Plain SSH, which answers only on the tailnet. Tailscale SSH doesn't need it."
	case name == "citrine-reboot.service":
		return "Reboots right away if a new Citrine is staged."
	case name == "citrine-backup.service":
		return "Dumps the databases and sends them and the rest of the nightly set to R2."
	case name == "citrine-scrub.service":
		return "Reads every block on the pool and checks it. It runs at idle priority but can take hours."
	case name == "citrine-pool-heal.service":
		return "Drops a missing device and rebalances back to RAID1 if it needs to. Hours, if it does anything."
	case name == "podman-auto-update.service":
		return "Pulls updated images and restarts what changed."
	case name == "rpm-ostreed-automatic.service":
		return "Checks for a new Citrine and applies it — the policy reboots as soon as it stages."
	case name == "citrine-site.service":
		return "Fetches the site, applies its citrine/ and builds what BUILD names if anything changed, restarting the containers it touches. citrine-site-poll starts it when the site's branch moves."
	case name == "citrine-site-poll.service":
		return "Asks the site's repository where its branch is and syncs if that isn't what's applied. A commit whose sync failed waits an hour unless a newer one lands."
	case name == "citrine-cloudflare.service":
		return "Deploys the shield's Worker and WAF rules to the zone while it's on, if they aren't already the ones there."
	case name == "citrine-first-boot.service":
		return "Does nothing once first boot has finished — System › f runs it again."
	}
	return "Runs systemctl " + verb + " " + name + "."
}

func (s *servicesTab) view(a *app, w, h int) string {
	t := a.t
	if !a.s.units.ok {
		if err := a.s.units.err; err != nil {
			return t.tint(lvBad, "Can't read systemd: ") + t.muted(errText(err))
		}
		return t.dim(a.spin.View() + " reading systemd")
	}
	rows := s.rows(a)
	s.sel = s.settle(rows, clamp(s.sel, 0, len(rows)-1), 1)
	names := make([]string, len(rows))
	c1 := 0
	for i, r := range rows {
		if r.header != "" {
			continue
		}
		names[i] = baseName(r.u.id)
		if r.timer {
			names[i] = timerName(r.u)
		}
		if r.u.user {
			names[i] += " ·user"
		}
		c1 = max(c1, width(names[i]))
	}
	c1 = clamp(c1, 14, 30)
	c2, c3 := 20, 16
	if 2+c1+2+c2+2+c3 > w-2 {
		c3 = 0
	}
	c1 = min(c1, w-2-2-c2-2)
	descW := w - 4 - (2 + c1 + 2 + c2 + 2 + c3)
	var lines []string
	selLine := 0
	for i, r := range rows {
		if r.header != "" {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			hdr := "  " + fit(t.heading(r.header), c1)
			switch r.header {
			case "Scheduled":
				hdr += "  " + fit(t.heading("next"), c2)
				if c3 > 0 {
					hdr += "  " + fit(t.heading("last run"), c3)
				}
			case "Services", "Failed":
				hdr += "  " + fit(t.heading("state"), c2)
				if c3 > 0 {
					hdr += "  " + fit(t.heading("since"), c3)
				}
			}
			lines = append(lines, hdr)
			continue
		}
		u := r.u
		name, user := strings.CutSuffix(names[i], " ·user")
		lead := "  "
		nameCell := t.soft(name)
		if i == s.sel {
			lead = t.accent("▌") + " "
			nameCell = t.bold(name)
			selLine = len(lines)
		}
		if user {
			nameCell += t.dim(" ·user")
		}
		var mid, last, desc string
		if r.timer {
			svc := a.unit(u.triggers)
			mid = t.accent(clock(u.next))
			if u.next.IsZero() {
				mid = t.dim("not scheduled")
			}
			lv, text := svc.lastRun()
			if svc.active == "activating" {
				lv, text = lvInfo, a.spin.View()+" running now"
			}
			last = t.dot(lv) + " " + t.tint(lv, text)
			desc = t.dim(svc.description)
		} else {
			lv, text := u.state()
			mid = t.dot(lv) + " " + t.tint(lv, text)
			if !u.since.IsZero() {
				last = t.muted(ago(u.since))
			}
			desc = t.dim(u.description)
		}
		line := lead + fit(nameCell, c1) + "  " + fit(mid, c2)
		if c3 > 0 {
			line += "  " + fit(last, c3)
		}
		if descW > 12 {
			line += "  " + fit(desc, descW)
		}
		lines = append(lines, line)
	}
	visible := max(1, h)
	if selLine < s.off {
		s.off = selLine
	}
	if selLine >= s.off+visible {
		s.off = selLine - visible + 1
	}
	s.off = clamp(s.off, 0, max(0, len(lines)-visible))
	window := lines[s.off:min(len(lines), s.off+visible)]
	if len(lines) > visible {
		t.scrollbar(window, s.off, len(lines), w-2)
	}
	return block(window, visible)
}
