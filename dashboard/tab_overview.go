package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type overviewTab struct{}

func (overviewTab) title() string { return "Overview" }

func (overviewTab) needs() []kind {
	return []kind{kSys, kContainers, kUserUnits, kUnits, kFailed, kOstree, kTailnet, kPool, kDrive, kAlerts, kTraffic}
}

func (overviewTab) keys(*app) []key.Binding {
	return []key.Binding{kNext, kTasks, kShell}
}

func (overviewTab) key(*app, tea.KeyPressMsg) tea.Cmd { return nil }

var timerNames = map[string]string{
	"citrine-backup.timer":        "R2 backup",
	"citrine-scrub.timer":         "pool scrub",
	"citrine-reboot.timer":        "reboot window",
	"citrine-cloudflare.timer":    "Cloudflare deploy",
	"rpm-ostreed-automatic.timer": "OS update",
	"podman-auto-update.timer":    "container updates",
}

func timerName(u unit) string {
	if n, ok := timerNames[u.id]; ok {
		return n
	}
	if u.description != "" {
		return u.description
	}
	return baseName(u.id)
}

func (o overviewTab) view(a *app, w, h int) string {
	t := a.t
	if w >= 96 {
		lw := max((w-2)/2, min(62, (w-2)*3/5))
		rw := w - lw - 2
		health, machine := o.health(a, lw-4), o.machine(a, rw-4, true)
		errs, next := o.errors(a, lw-4), o.upcoming(a, rw-4)
		top := max(len(health), len(machine)) + 2
		bottom := clamp(max(len(next), len(errs))+2, 3, max(3, h-top-1))
		return lipgloss.JoinHorizontal(lipgloss.Top,
			t.box(t.bold("Health"), health, lw, top), "  ",
			t.box(t.bold("Machine"), machine, rw, top)) + "\n\n" +
			lipgloss.JoinHorizontal(lipgloss.Top,
				t.box(t.bold("Recent errors"), errs, lw, bottom), "  ",
				t.box(t.bold("Coming up"), next, rw, bottom))
	}
	parts := []string{
		t.box(t.bold("Health"), o.health(a, w-4), w, 0),
		t.box(t.bold("Machine"), o.machine(a, w-4, false), w, 0),
		t.box(t.bold("Recent errors"), o.errors(a, w-4), w, 0),
		t.box(t.bold("Coming up"), o.upcoming(a, w-4), w, 0),
	}
	return strings.Join(parts, "\n")
}

func (overviewTab) errors(a *app, iw int) []string {
	t := a.t
	r := a.s.alerts
	switch {
	case !r.ok && r.err != nil:
		return []string{t.dim(errText(r.err))}
	case !r.ok:
		return []string{t.dim(a.spin.View() + " reading the journal")}
	case len(r.val) == 0:
		return []string{t.dot(lvOK) + " " + t.muted("none in the last day")}
	}
	var out []string
	for i := len(r.val) - 1; i >= 0 && len(out) < 6; i-- {
		e := r.val[i]
		when := e.at.Local().Format("15:04")
		if !sameDay(e.at, time.Now()) {
			when = e.at.Local().Format("Mon")
		}
		src := ""
		if e.source != "" {
			src = t.faint(clip(baseName(e.source), 14)) + " "
		}
		out = append(out, clip(t.dim(when)+" "+src+t.tint(lvBad, e.msg), iw))
	}
	return out
}

func (overviewTab) machine(a *app, iw int, roomy bool) []string {
	t := a.t
	s := &a.s
	detail := 17
	if iw < 44 {
		detail = 0
	}
	gw := iw - 9 - 1 - 4 - detail
	if detail > 0 {
		gw -= 2
	}
	gw = max(6, gw)
	gaugeRow := func(label string, frac float64, shown float64, right string) string {
		row := fit(t.faint(label), 9) + t.gauge(shown, gw, t.usage(frac)) + " " + fitRight(t.text(pct(frac)), 4)
		if detail > 0 {
			row += "  " + fit(right, detail)
		}
		return row
	}
	var out []string
	out = append(out, gaugeRow("CPU", s.cpu, a.shown("cpu"), t.spark(s.cpuHist, detail, 1, t.c.accent)))
	mem := ""
	if s.sys.memTotal > 0 {
		mem = t.muted(pair(s.sys.memTotal-min(s.sys.memTotal, s.sys.memAvail), s.sys.memTotal))
	}
	out = append(out, gaugeRow("Memory", s.mem, a.shown("mem"), mem))
	p := s.pool.val
	if p.mounted {
		out = append(out, gaugeRow("Pool", p.frac(), a.shown("pool"), t.muted(pair(p.used(), p.size))))
	} else {
		out = append(out, fit(t.faint("Pool"), 9)+t.tint(lvBad, "not mounted"))
	}
	net := t.dim("↓ ") + t.muted(rate(s.rx)) + t.dim("  ↑ ") + t.muted(rate(s.tx))
	if detail > 0 {
		net = fit(net, gw+5) + "  " + t.spark(s.netHist, detail, 0, t.c.info)
	}
	out = append(out, fit(t.faint("Network"), 9)+net)
	var facts []string
	if s.sys.temp > 0 {
		facts = append(facts, t.dim("temp ")+t.tint(tempLevel(s.sys.temp), fmt.Sprintf("%.0f°C", s.sys.temp)))
	}
	facts = append(facts, t.dim("load ")+t.muted(fmt.Sprintf("%.2f %.2f %.2f", s.sys.load[0], s.sys.load[1], s.sys.load[2])))
	if s.sys.cores > 0 {
		facts = append(facts, t.dim("cores ")+t.muted(fmt.Sprint(s.sys.cores)))
	}
	if roomy {
		out = append(out, "")
	}
	out = append(out, clip(strings.Join(facts, t.edge("  ·  ")), iw))
	if k := kernel(); k != "" {
		out = append(out, clip(t.dim("kernel ")+t.muted(k), iw))
	}
	return out
}

func tempLevel(c float64) level {
	switch {
	case c >= 85:
		return lvBad
	case c >= 72:
		return lvWarn
	}
	return lvOK
}

func (o overviewTab) health(a *app, iw int) []string {
	t := a.t
	row := func(lv level, label, text string) string {
		return clip(t.dot(lv)+" "+fit(t.soft(label), 12)+text, iw)
	}
	pending := func(label string) string { return row(lvNone, label, t.dim(a.spin.View()+" checking")) }
	var out []string

	if r := a.s.pods; r.ok {
		cs := a.containers()
		up, sick := 0, []string{}
		for _, c := range cs {
			lv, state := c.state()
			if lv == lvOK {
				up++
			} else {
				sick = append(sick, c.q.name+" "+state)
			}
		}
		switch {
		case len(cs) == 0:
			out = append(out, row(lvWarn, "Containers", t.muted("no quadlets found")))
		case len(sick) == 0:
			out = append(out, row(lvOK, "Containers", t.muted(fmt.Sprintf("%d of %d up", up, len(cs)))))
		default:
			lv := lvWarn
			for _, s := range sick {
				if strings.HasSuffix(s, "failed") || strings.HasSuffix(s, "unhealthy") || strings.HasSuffix(s, "stopped") {
					lv = lvBad
				}
			}
			out = append(out, row(lv, "Containers", t.muted(strings.Join(sick, ", "))))
		}
	} else if r.err != nil {
		out = append(out, row(lvBad, "Containers", t.muted(errText(r.err))))
	} else {
		out = append(out, pending("Containers"))
	}

	if r := a.s.pool; r.ok {
		lv, text := r.val.health()
		if r.val.mounted {
			text += t.dim(" · ") + pct(r.val.frac()) + " used"
		}
		out = append(out, row(lv, "Pool", t.muted(text)))
	} else {
		out = append(out, pending("Pool"))
	}

	if r := a.s.tailnet; r.ok {
		tn := r.val
		lv, text := lvOK, "online · "+tn.ip4()
		if tn.state != "Running" {
			lv, text = lvBad, strings.ToLower(tn.state)
		} else if !tn.self.online {
			lv, text = lvWarn, "connected, control plane unreachable"
		}
		out = append(out, row(lv, "Tailnet", t.muted(text)))
	} else if r.err != nil {
		out = append(out, row(lvBad, "Tailnet", t.muted(errText(r.err))))
	} else {
		out = append(out, pending("Tailnet"))
	}

	out = append(out, o.backups(a, row))
	out = append(out, o.updates(a, row))

	if r := a.s.traffic; r.ok {
		v := r.val
		out = append(out, row(lvInfo, "Cloudflare", t.muted(plural(v.visits, "visit", "visits")+" · "+commas(v.stopped)+" stopped")+t.dim(" · "+windows[a.visitIdx].name)))
	} else if r.err != nil {
		out = append(out, row(lvNone, "Cloudflare", t.muted(errText(r.err))))
	} else {
		out = append(out, pending("Cloudflare"))
	}

	if r := a.s.failed; r.ok {
		if n := len(r.val); n > 0 {
			names := make([]string, 0, n)
			for _, f := range r.val {
				names = append(names, baseName(f.name))
			}
			out = append(out, row(lvBad, "Units", t.muted(plural(n, "failed: ", "failed: ")+strings.Join(names, ", "))))
		} else {
			out = append(out, row(lvOK, "Units", t.muted("nothing failed")))
		}
	} else {
		out = append(out, pending("Units"))
	}
	return out
}

func (overviewTab) backups(a *app, row func(level, string, string) string) string {
	t := a.t
	if !a.s.units.ok {
		return row(lvNone, "Backups", t.dim(a.spin.View()+" checking"))
	}
	u := a.unit("citrine-backup.service")
	lv := lvOK
	r2 := "R2 never"
	switch {
	case u.active == "activating":
		lv, r2 = lvInfo, "R2 running now"
	case u.exited.IsZero():
		lv = lvWarn
	case u.result != "success":
		lv, r2 = lvBad, "R2 failed "+ago(u.exited)
	default:
		r2 = "R2 " + ago(u.exited)
		if time.Since(u.exited) > 36*time.Hour {
			lv = lvWarn
		}
	}
	d := a.s.drive.val
	dr := "drive never"
	switch {
	case d.mounted:
		dr = "drive backing up now"
	case !d.last.IsZero():
		dr = "drive " + ago(d.last)
		if time.Since(d.last) > 60*24*time.Hour {
			lv = worst(lv, lvWarn)
		}
	}
	return row(lv, "Backups", t.muted(r2+" · "+dr))
}

func (overviewTab) updates(a *app, row func(level, string, string) string) string {
	t := a.t
	r := a.s.ostree
	if !r.ok {
		if r.err != nil {
			return row(lvNone, "Updates", t.muted(errText(r.err)))
		}
		return row(lvNone, "Updates", t.dim(a.spin.View()+" checking"))
	}
	booted, _ := r.val.booted()
	switch staged, ok := r.val.staged(); {
	case r.val.busy:
		return row(lvInfo, "Updates", t.muted("rpm-ostree is working"))
	case ok:
		next := a.unit("citrine-reboot.timer").next
		return row(lvInfo, "Updates", t.muted(staged.name()+" staged · reboots "+clock(next)))
	case booted.tag() != "" && booted.tag() != "latest":
		return row(lvWarn, "Updates", t.muted("pinned to "+booted.tag()+" — nothing updates"))
	}
	return row(lvOK, "Updates", t.muted("following latest · nothing staged"))
}

func (overviewTab) upcoming(a *app, iw int) []string {
	t := a.t
	var timers []unit
	for _, u := range a.s.units.val.units {
		if strings.HasSuffix(u.id, ".timer") && !u.next.IsZero() && u.id != "citrine-site-poll.timer" {
			timers = append(timers, u)
		}
	}
	for _, u := range a.s.userUnits.val.units {
		if strings.HasSuffix(u.id, ".timer") && !u.next.IsZero() {
			timers = append(timers, u)
		}
	}
	sort.Slice(timers, func(i, j int) bool {
		if !timers[i].next.Equal(timers[j].next) {
			return timers[i].next.Before(timers[j].next)
		}
		return timers[i].id < timers[j].id
	})
	if len(timers) == 0 {
		if !a.s.units.ok {
			return []string{t.dim(a.spin.View() + " reading timers")}
		}
		return []string{t.dim("no timers scheduled")}
	}
	tw := 0
	for _, u := range timers {
		tw = max(tw, width(clock(u.next)))
	}
	var out []string
	for _, u := range timers {
		out = append(out, clip(fit(t.accent(clock(u.next)), tw)+"  "+t.soft(timerName(u)), iw))
	}
	return out
}
