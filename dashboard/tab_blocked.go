package main

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

var kBlocker = bind("b", "blocker", "b")

type blockedTab struct {
	sel, off int
	by       string
}

func (blockedTab) title() string { return "Blocked" }

func (blockedTab) needs() []kind { return []kind{kTraffic, kStops} }

func (blockedTab) keys(*app) []key.Binding { return []key.Binding{kWindow, kBlocker} }

func (blockedTab) selects(msg tea.KeyPressMsg) bool { return key.Matches(msg, kSelect) }

func (e shieldEvent) blocker() string {
	if e.rule != "" && (e.source == "firewallCustom" || e.source == "shield") {
		return e.rule
	}
	return e.what
}

func blockers(es []shieldEvent) []count {
	n := map[string]int{}
	for _, e := range es {
		n[e.blocker()]++
	}
	out := make([]count, 0, len(n))
	for label, c := range n {
		out = append(out, count{label: label, n: c})
	}
	slices.SortFunc(out, func(a, b count) int {
		if a.n != b.n {
			return b.n - a.n
		}
		return strings.Compare(a.label, b.label)
	})
	return out
}

func (b *blockedTab) shown(a *app) []shieldEvent {
	es := a.s.stops.val
	if !slices.ContainsFunc(es, func(e shieldEvent) bool { return e.blocker() == b.by }) {
		b.by = ""
	}
	if b.by == "" {
		return es
	}
	return slices.DeleteFunc(slices.Clone(es), func(e shieldEvent) bool { return e.blocker() != b.by })
}

func (b *blockedTab) key(a *app, msg tea.KeyPressMsg) tea.Cmd {
	n := len(b.shown(a))
	switch {
	case key.Matches(msg, kWindow):
		b.sel = 0
		return a.cycleWindow()
	case key.Matches(msg, kBlocker):
		by := blockers(a.s.stops.val)
		i := slices.IndexFunc(by, func(c count) bool { return c.label == b.by })
		b.by, b.sel, b.off = "", 0, 0
		if i+1 < len(by) {
			b.by = by[i+1].label
		}
	case key.Matches(msg, kUp):
		b.sel = clamp(b.sel-1, 0, n-1)
	case key.Matches(msg, kDown):
		b.sel = clamp(b.sel+1, 0, n-1)
	}
	return nil
}

func (b *blockedTab) view(a *app, w, h int) string {
	t := a.t
	head := spread(a.windowPicker(), t.dim("who Cloudflare stopped · w cycles · b picks a blocker"), w)
	out := []string{head, ""}
	r := a.s.traffic
	switch {
	case !r.ok && r.err != nil:
		out = append(out, t.tint(lvBad, "Can't read Cloudflare's analytics: ")+t.muted(errText(r.err)))
	case !r.ok:
		out = append(out, t.dim(a.spin.View()+" asking Cloudflare about the last "+windows[a.visitIdx].name))
	default:
		lines := append([]string{t.text(plural(r.val.stopped, "request", "requests")) + t.muted(" blocked, challenged or trapped")},
			a.trafficSpark(r.val, r.val.histStopped, w-4, t.c.bad)...)
		out = append(out, t.box(t.bold("Stopped"), lines, w, 0))
	}
	out = append(out, b.events(a, w, h))
	var charts []chart
	if r.ok {
		charts = append(charts, chart{"Security", r.val.security, t.c.bad})
	}
	if a.s.stops.ok {
		charts = append(charts, chart{"Blockers", blockers(a.s.stops.val), t.c.warn})
	}
	out = append(out, a.charts(charts, w)...)
	return strings.Join(out, "\n")
}

func (b *blockedTab) events(a *app, w, h int) string {
	t := a.t
	r := a.s.stops
	title := t.bold("Stops") + t.dim(" · what Cloudflare knew")
	switch {
	case !r.ok && r.err != nil:
		return t.box(title, []string{t.tint(lvBad, "Can't read Cloudflare's security events: ") + t.muted(errText(r.err))}, w, 0)
	case !r.ok:
		return t.box(title, []string{t.dim(a.spin.View() + " asking Cloudflare for its security events")}, w, 0)
	case len(r.val) == 0:
		return t.box(title, []string{t.dim("nothing stopped")}, w, 0)
	}
	es := b.shown(a)
	b.sel = clamp(b.sel, 0, len(es)-1)
	title = t.bold("Stops") + t.dim(" · the latest "+plural(len(es), "event", "events"))
	if b.by != "" {
		title = t.bold("Stops") + t.dim(" · the latest "+plural(len(es), "event", "events")+" from ") + t.soft(b.by) + t.dim(" of "+commas(len(r.val)))
	}
	cols := []col{{title: "when", w: 9}, {title: "what", w: 22}, {title: "from", w: 4}, {title: "address", w: 22}, {title: "path", w: 0}}
	var rows [][]string
	for _, e := range es {
		rows = append(rows, []string{t.muted(ago(e.at)), t.soft(e.blocker()), t.muted(e.country), t.soft(e.ip), t.dim(e.path)})
	}
	lw, dw := w, w
	if w >= 110 {
		lw = (w - 2) * 3 / 5
		dw = w - lw - 2
	}
	list := t.table(cols, rows, b.sel, lw-4, clamp(h/2, 8, 20), &b.off)
	e := es[b.sel]
	detail := []string{
		field(t, "when", stamp(e.at)+" · "+ago(e.at), dw-4),
		field(t, "what", e.what, dw-4),
		field(t, "rule", e.rule, dw-4),
		field(t, "ray", e.ray, dw-4),
		field(t, "address", e.ip, dw-4),
		field(t, "network", e.asn, dw-4),
		field(t, "country", e.country, dw-4),
	}
	detail = append(detail, wrappedField(t, "request", strings.TrimSpace(e.method+" "+e.host+e.path), dw-4)...)
	detail = append(detail, wrappedField(t, "user-agent", e.agent, dw-4)...)
	return a.listWithDetail(title, list, t.bold(e.ip), detail, w)
}
