package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

type allowedTab struct {
	sel, off int
}

func (allowedTab) title() string { return "Allowed" }

func (allowedTab) needs() []kind { return []kind{kTraffic, kVisits} }

func (allowedTab) keys(*app) []key.Binding { return []key.Binding{kWindow} }

func (allowedTab) selects(msg tea.KeyPressMsg) bool { return key.Matches(msg, kSelect) }

func (al *allowedTab) key(a *app, msg tea.KeyPressMsg) tea.Cmd {
	n := len(a.s.visits.val.visitors)
	switch {
	case key.Matches(msg, kWindow):
		al.sel = 0
		return a.cycleWindow()
	case key.Matches(msg, kUp):
		al.sel = clamp(al.sel-1, 0, n-1)
	case key.Matches(msg, kDown):
		al.sel = clamp(al.sel+1, 0, n-1)
	}
	return nil
}

func (al *allowedTab) view(a *app, w, h int) string {
	t := a.t
	head := spread(a.windowPicker(), t.dim("who Cloudflare let through · w cycles"), w)
	out := []string{head, "", al.traffic(a, w), al.visitors(a, w, h)}
	if r := a.s.traffic; r.ok {
		out = append(out, a.charts([]chart{
			{"Countries", r.val.countries, t.c.accent},
			{"Paths", r.val.paths, t.c.accent},
			{"Status codes", r.val.statuses, t.c.info},
			{"Cache", r.val.cache, t.c.info},
			{"Verified bots", r.val.bots, t.c.warn},
		}, w)...)
	}
	return strings.Join(out, "\n")
}

func (allowedTab) traffic(a *app, w int) string {
	t := a.t
	r := a.s.traffic
	switch {
	case !r.ok && r.err != nil:
		return t.tint(lvBad, "Can't read Cloudflare's analytics: ") + t.muted(errText(r.err))
	case !r.ok:
		return t.dim(a.spin.View() + " asking Cloudflare about the last " + windows[a.visitIdx].name)
	}
	v := r.val
	lines := []string{
		t.text(plural(v.requests, "request", "requests")) + t.dim(" · ") + t.text(plural(v.visits, "visit", "visits")) + t.dim(" · ") + t.text(plural(v.pageViews, "page view", "page views")),
		t.muted(size(v.bytes) + " served · cache answered " + pct(share(float64(v.cachedReqs), float64(v.requests))) + " of requests, " + pct(share(float64(v.cachedBytes), float64(v.bytes))) + " of bytes"),
		t.muted(fmt.Sprintf("4xx %.1f%% · 5xx %.1f%%", v.status4xx*100, v.status5xx*100)),
	}
	if v.originP50 > 0 {
		lines[2] += t.muted(fmt.Sprintf(" · origin p50 %.0f ms, p95 %.0f ms", v.originP50, v.originP95))
	}
	lines = append(lines, a.trafficSpark(v, v.hist, w-4, t.c.accent)...)
	return t.box(t.bold("Traffic"), lines, w, 0)
}

func clipped(lines []string, w int) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = clip(l, w)
	}
	return out
}

func missedSince(tr traffic, v visits) (time.Time, bool) {
	var logged time.Time
	for _, vis := range v.visitors {
		if vis.last.After(logged) {
			logged = vis.last
		}
	}
	return tr.lastServed, !tr.lastServed.IsZero() && tr.lastServed.Sub(logged) > 10*time.Minute
}

func (al *allowedTab) visitors(a *app, w, h int) string {
	t := a.t
	r := a.s.visits
	title := t.bold("Visitors") + t.dim(" · what reached the site")
	switch {
	case !r.ok && r.err != nil:
		return t.box(title, []string{t.tint(lvBad, "Can't read the visit log: ") + t.muted(errText(r.err))}, w, 0)
	case !r.ok:
		return t.box(title, []string{t.dim(a.spin.View() + " reading the visit log for the last " + windows[a.visitIdx].name)}, w, 0)
	}
	var gap []string
	if tr := a.s.traffic; tr.ok {
		if at, missed := missedSince(tr.val, r.val); missed {
			gap = []string{t.tint(lvWarn, "Cloudflare served requests at "+stamp(at)+" the visit log doesn't have"),
				t.muted("citrine-visits' tail may be down, or the shield isn't deployed"), ""}
		}
	}
	if len(r.val.visitors) == 0 {
		return t.box(title, append(clipped(gap, w-4), t.dim("nobody yet")), w, 0)
	}
	vs := r.val.visitors
	al.sel = clamp(al.sel, 0, len(vs)-1)
	title = t.bold("Visitors") + t.dim(" · "+plural(r.val.requests, "request", "requests")+" from "+plural(len(vs), "address", "addresses"))
	cols := []col{{title: "seen", w: 9}, {title: "from", w: 4}, {title: "address", w: 22}, {title: "reqs", w: 5, right: true}, {title: "agent", w: 0}}
	var rows [][]string
	for _, v := range vs {
		rows = append(rows, []string{t.muted(ago(v.last)), t.muted(v.country), t.soft(v.ip), t.text(commas(v.requests)), t.dim(agentName(v.agent))})
	}
	lw, dw := w, w
	if w >= 110 {
		lw = (w - 2) * 3 / 5
		dw = w - lw - 2
	}
	list := t.table(cols, rows, al.sel, lw-4, clamp(h/2, 8, 20), &al.off)
	list = append(clipped(gap, lw-4), list...)
	return a.listWithDetail(title, list, t.bold(vs[al.sel].ip), al.detail(t, vs[al.sel], dw-4), w)
}

func (allowedTab) detail(t theme, v visitor, w int) []string {
	lines := []string{
		field(t, "first seen", stamp(v.first), w),
		field(t, "last seen", stamp(v.last)+" · "+ago(v.last), w),
		field(t, "requests", commas(v.requests)+" · last answered "+fmt.Sprint(v.status), w),
	}
	lines = append(lines, wrappedField(t, "user-agent", v.agent, w)...)
	if v.referer != "" {
		lines = append(lines, wrappedField(t, "referer", v.referer, w)...)
	}
	names := make([]string, 0, len(v.cf))
	for name := range v.cf {
		names = append(names, name)
	}
	sort.Strings(names)
	lines = append(lines, "", t.heading("cf headers"))
	for _, name := range names {
		lines = append(lines, field(t, name, v.cf[name], w))
	}
	lines = append(lines, "", t.heading("latest paths"))
	paths := slices.Clone(v.paths)
	slices.Reverse(paths)
	for _, p := range paths {
		lines = append(lines, t.soft(p))
	}
	return lines
}

func agentName(ua string) string {
	if _, rest, ok := strings.Cut(ua, "compatible; "); ok {
		name, _, _ := strings.Cut(rest, ";")
		name, _, _ = strings.Cut(name, ")")
		return name
	}
	if ua == "" {
		return "(none)"
	}
	return ua
}
