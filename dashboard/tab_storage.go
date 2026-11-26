package main

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

type storageTab struct {
	sel, off int
}

var (
	kAdopt    = bind("a", "adopt disk", "a")
	kScrubNow = bind("s", "scrub", "s")
	kHeal     = bind("H", "heal", "H")
)

func (storageTab) title() string { return "Storage" }

func (storageTab) needs() []kind { return []kind{kPool, kScrub, kDisks, kSmart, kUnits} }

func (s *storageTab) keys(a *app) []key.Binding {
	var out []key.Binding
	if d, ok := s.current(a); ok && d.adoptable() {
		out = append(out, kAdopt)
	}
	return append(out, kScrubNow, kHeal)
}

func (s *storageTab) current(a *app) (disk, bool) {
	ds := a.s.disks.val
	if len(ds) == 0 {
		return disk{}, false
	}
	s.sel = clamp(s.sel, 0, len(ds)-1)
	return ds[s.sel], true
}

func (storageTab) selects(msg tea.KeyPressMsg) bool { return key.Matches(msg, kSelect) }

func (s *storageTab) key(a *app, msg tea.KeyPressMsg) tea.Cmd {
	n := len(a.s.disks.val)
	switch {
	case key.Matches(msg, kUp):
		s.sel = clamp(s.sel-1, 0, n-1)
	case key.Matches(msg, kDown):
		s.sel = clamp(s.sel+1, 0, n-1)
	case key.Matches(msg, kScrubNow):
		return a.confirm("Scrub the pool now?",
			"Reads every block and checks it against its checksum, repairing from the mirror. Idle priority, but it can take hours.",
			"Scrub", false, startCmd(spec{
				key: "scrub", title: "Scrub the pool", root: true,
				argv: systemctl(false, "start", "citrine-scrub.service"), unit: "citrine-scrub.service",
				done: "Scrub finished", after: []kind{kScrub, kPool},
			}))
	case key.Matches(msg, kHeal):
		return a.confirm("Heal the pool?",
			"Drops a device that went missing — adding the internal disk back first if it's absent — and converts anything unmirrored back to RAID1. This is what boot does; it only acts if something's wrong.",
			"Heal", false, startCmd(spec{
				key: "heal", title: "Heal the pool", root: true,
				argv: systemctl(false, "start", "citrine-pool-heal.service"), unit: "citrine-pool-heal.service",
				done: "Pool checked", after: []kind{kPool},
			}))
	case key.Matches(msg, kAdopt):
		d, ok := s.current(a)
		if !ok || !d.adoptable() {
			return nil
		}
		desc := strings.TrimSpace(size(d.size) + " " + d.model)
		body := fmt.Sprintf("Everything on %s (%s) is erased and it joins %s. The pool converts to RAID1 in the background.", d.name, desc, a.cfg["POOL_LABEL"])
		if d.role != "blank" {
			body = "It isn't blank — " + strings.Join(append(d.fstypes, d.labels...), ", ") + ". " + body
		}
		argv, unit := transient("adopt-"+d.name, "Adopt "+d.name, false, libexec+"/usb-adopt", d.name, "force")
		return a.typed("Adopt "+d.name+" into the pool?", body+" Type its name to go ahead.", d.name, startCmd(spec{
			key: "adopt", title: "Adopt " + d.name, root: true, argv: argv, unit: unit,
			done: d.name + " joined the pool", after: []kind{kPool, kDisks},
		}))
	}
	return nil
}

func (s *storageTab) view(a *app, w, h int) string {
	poolBox := s.pool(a, w)
	disks := s.disks(a, w, max(4, h-strings.Count(poolBox, "\n")-3))
	return poolBox + "\n\n" + disks
}

func (storageTab) pool(a *app, w int) string {
	t := a.t
	r := a.s.pool
	title := t.bold("Pool") + "  " + t.faint(a.cfg["POOL_LABEL"])
	if !r.ok {
		msg := t.dim(a.spin.View() + " reading the pool")
		if r.err != nil {
			msg = t.tint(lvBad, errText(r.err))
		}
		return t.panel(title, msg, w)
	}
	p := r.val
	iw := w - 4
	if !p.mounted {
		return t.panel(title, t.tint(lvBad, "Nothing is mounted at "+p.point+".")+"\n"+t.muted("Containers that keep data on the pool refuse to start until it is."), w)
	}
	lv, state := p.health()
	var profs []string
	for _, al := range p.alloc {
		profs = append(profs, al.profile+" "+al.kind)
	}
	lines := []string{
		t.dot(lv) + " " + t.tint(lv, state) + t.dim("  "+p.point+" · "+plural(len(p.devices), "device", "devices")+" · "+strings.Join(profs, ", ")),
	}
	right := t.text(pct(p.frac())) + "  " + t.muted(size(p.used())) + t.dim(" of "+size(p.size)+" · "+size(p.free)+" free")
	gw := max(10, iw-width(right)-2)
	lines = append(lines, t.gauge(a.shown("pool"), gw, t.usage(p.frac()))+"  "+right, "")

	cols := []col{{title: "device", w: 0}, {title: "size", w: 9, right: true}, {title: "used", w: 9, right: true}, {title: "errors", w: 8, right: true}}
	var rows [][]string
	for _, d := range p.devices {
		name := d.path
		if name == "" {
			name = fmt.Sprintf("devid %d", d.devid)
		}
		cells := []string{t.soft(name), t.muted(sizeOr(d.size)), t.muted(sizeOr(d.used)), t.check(d.errorCount() == 0)}
		if d.errorCount() > 0 {
			cells[3] = t.tint(lvBad, commas(int(d.errorCount())))
		}
		if d.missing {
			cells[0] = t.tint(lvBad, name+" — missing")
		}
		rows = append(rows, cells)
	}
	lines = append(lines, t.table(cols, rows, -1, iw, len(rows)+1, nil)...)
	if !p.detailed && p.detail != nil {
		lines = append(lines, t.dim("per-device sizes need root: "+errText(p.detail)))
	}
	lines = append(lines, "", storageScrub(a, iw))
	return t.panel(title, strings.Join(lines, "\n"), w)
}

func sizeOr(n uint64) string {
	if n == 0 {
		return "—"
	}
	return size(n)
}

func storageScrub(a *app, iw int) string {
	t := a.t
	u := a.unit("citrine-scrub.service")
	next := a.unit("citrine-scrub.timer").next
	label := t.faint("scrub  ")
	sc := a.s.scrub
	if u.active == "activating" || (sc.ok && sc.val.status == "running") {
		left := ""
		if sc.val.left != "" {
			left = t.dim(" · " + sc.val.left + " left")
		}
		right := t.text(pct(sc.val.progress)) + left
		return label + t.gauge(a.shown("scrub"), max(8, iw-width(label)-width(right)-2), t.c.info) + "  " + right
	}
	var parts []string
	switch {
	case sc.ok && sc.val.status != "":
		lv := lvOK
		if sc.val.errors != "" && !strings.Contains(sc.val.errors, "no errors") {
			lv = lvBad
		}
		when := ""
		if !sc.val.started.IsZero() {
			when = " " + ago(sc.val.started)
		}
		parts = append(parts, t.dot(lv)+" "+t.muted(sc.val.status+when))
		if sc.val.errors != "" {
			parts = append(parts, t.tint(lv, sc.val.errors))
		}
	case !u.exited.IsZero():
		lv, text := u.lastRun()
		parts = append(parts, t.dot(lv)+" "+t.muted(text))
	case sc.err != nil:
		parts = append(parts, t.dim(errText(sc.err)))
	}
	if !next.IsZero() {
		parts = append(parts, t.dim("next ")+t.accent(clock(next)))
	}
	return label + strings.Join(parts, t.dim(" · "))
}

func (s *storageTab) disks(a *app, w, h int) string {
	t := a.t
	r := a.s.disks
	if !r.ok {
		if r.err != nil {
			return t.tint(lvBad, "Can't list disks: ") + t.muted(errText(r.err))
		}
		return t.dim(a.spin.View() + " listing disks")
	}
	cols := []col{{title: "disk", w: 10}, {title: "size", w: 8, right: true}, {title: "model", w: 0}, {title: "bus", w: 6}, {title: "role", w: 13}, {title: "health", w: 26}}
	if w < 90 {
		cols = []col{{title: "disk", w: 10}, {title: "size", w: 8, right: true}, {title: "role", w: 13}, {title: "health", w: 0}}
	}
	var rows [][]string
	for i, d := range r.val {
		name := t.soft(d.name)
		if i == s.sel {
			name = t.bold(d.name)
		}
		bus := d.tran
		if bus == "" {
			bus = "—"
		}
		role := t.muted(d.role)
		switch {
		case d.pool:
			role = t.accent(d.role)
		case d.backup:
			role = t.tint(lvInfo, d.role)
		}
		health := s.health(a, d)
		if len(cols) == 4 {
			rows = append(rows, []string{name, t.muted(size(d.size)), role, health})
		} else {
			rows = append(rows, []string{name, t.muted(size(d.size)), t.faint(d.model), t.dim(bus), role, health})
		}
	}
	s.sel = clamp(s.sel, 0, len(rows)-1)
	return strings.Join(t.table(cols, rows, s.sel, w, h, &s.off), "\n")
}

func (storageTab) health(a *app, d disk) string {
	t := a.t
	sm, ok := a.s.smart.val[d.name]
	switch {
	case !ok && a.loading(kSmart):
		return t.dim(a.spin.View() + " asking")
	case !ok:
		return t.dim("—")
	case sm.err != nil && !sm.known:
		return t.dot(lvNone) + " " + t.dim(clip(errText(sm.err), 22))
	case !sm.known:
		return t.dot(lvNone) + " " + t.dim("no SMART")
	}
	lv, text := lvOK, "ok"
	if !sm.ok {
		lv, text = lvBad, "failing"
	}
	parts := []string{t.tint(lv, text)}
	if sm.temp > 0 {
		parts = append(parts, t.muted(fmt.Sprintf("%d°C", sm.temp)))
	}
	if sm.wear > 0 {
		parts = append(parts, t.muted(fmt.Sprintf("%d%% worn", sm.wear)))
	} else if sm.hours > 0 {
		parts = append(parts, t.muted(span(time.Duration(sm.hours)*time.Hour)))
	}
	if sm.realloc > 0 {
		parts = append(parts, t.tint(lvWarn, fmt.Sprintf("%d realloc", sm.realloc)))
	}
	return t.dot(lv) + " " + strings.Join(parts, t.dim(" · "))
}
