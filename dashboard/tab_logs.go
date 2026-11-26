package main

import (
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type source struct {
	label string
	unit  string
	user  bool
}

type logsLoaded struct {
	gen     int
	entries []entry
	err     error
	more    bool
}

type logsTick struct{ gen int }

type logsTab struct {
	sel       int
	off       int
	extra     []source
	cur       source
	loaded    bool
	ready     bool
	entries   []entry
	err       error
	prio      int
	follow    bool
	filter    textinput.Model
	filtering bool
	gen       int
	ticking   bool
	lastH     int
}

var (
	kFollow = bind("f", "follow", "f")
	kPrio   = bind("p", "priority", "p")
	kFind   = bind("/", "filter", "/")
	kSource = bind("↑↓", "source", "up", "down")
)

func newLogsTab() *logsTab {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.Placeholder = "filter"
	return &logsTab{follow: true, off: -1, filter: ti}
}

func (*logsTab) title() string { return "Logs" }

func (*logsTab) needs() []kind { return []kind{kUnits, kContainers} }

func (l *logsTab) capturing() bool { return l.filtering }

func (l *logsTab) keys(*app) []key.Binding {
	if l.filtering {
		return []key.Binding{bind("enter", "apply", "enter"), bind("esc", "clear", "esc")}
	}
	follow := kFollow
	if l.follow {
		follow = bind("f", "pause", "f")
	}
	return []key.Binding{kSource, follow, kPrio, kFind}
}

func (l *logsTab) sources(a *app) []source {
	out := []source{{label: "Everything", unit: ""}, {label: "Mine", unit: "", user: true}}
	seen := map[string]bool{}
	add := func(s source) {
		k := s.unit + map[bool]string{true: "@user"}[s.user]
		if s.unit != "" && seen[k] {
			return
		}
		seen[k] = true
		out = append(out, s)
	}
	var own []string
	for _, n := range a.s.units.val.names {
		if strings.HasPrefix(n, "citrine-") && strings.HasSuffix(n, ".service") {
			own = append(own, n)
		}
	}
	sort.Strings(own)
	for _, n := range own {
		add(source{label: baseName(n), unit: n})
	}
	for _, q := range a.s.pods.val.quadlets {
		add(source{label: q.name, unit: q.unit, user: true})
	}
	for _, n := range []string{"rpm-ostreed-automatic.service", "podman-auto-update.service", "tailscaled.service", "smb.service", "sshd.service"} {
		add(source{label: baseName(n), unit: n, user: n == "podman-auto-update.service"})
	}
	for _, s := range l.extra {
		add(s)
	}
	return out
}

func (l *logsTab) activate(a *app) tea.Cmd {
	srcs := l.sources(a)
	l.sel = clamp(l.sel, 0, len(srcs)-1)
	var cmds []tea.Cmd
	if !l.loaded || srcs[l.sel] != l.cur {
		cmds = append(cmds, l.load(srcs[l.sel]))
	}
	if !l.ticking {
		l.ticking = true
		gen := l.gen
		cmds = append(cmds, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return logsTick{gen} }))
	}
	return tea.Batch(cmds...)
}

func (l *logsTab) load(s source) tea.Cmd {
	l.gen++
	l.cur, l.loaded, l.ready, l.err, l.off = s, true, false, nil, -1
	l.entries = nil
	gen, q := l.gen, query{user: s.user, unit: s.unit, prio: l.prio, lines: 600}
	return func() tea.Msg {
		es, err := readJournal(q)
		return logsLoaded{gen: gen, entries: es, err: err}
	}
}

func (l *logsTab) poll() tea.Cmd {
	gen := l.gen
	q := query{user: l.cur.user, unit: l.cur.unit, prio: l.prio, lines: 600}
	more := len(l.entries) > 0 && l.entries[len(l.entries)-1].cursor != ""
	if more {
		q.after, q.lines = l.entries[len(l.entries)-1].cursor, 0
	}
	return func() tea.Msg {
		es, err := readJournal(q)
		return logsLoaded{gen: gen, entries: es, err: err, more: more}
	}
}

func (l *logsTab) handle(a *app, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case logsLoaded:
		if msg.gen != l.gen {
			return nil
		}
		if msg.err != nil && !msg.more {
			l.err = msg.err
			return nil
		}
		if msg.more {
			l.entries = append(l.entries, msg.entries...)
			if len(l.entries) > 5000 {
				l.entries = l.entries[len(l.entries)-5000:]
			}
		} else {
			l.entries, l.ready = msg.entries, true
		}
	case logsTick:
		active := a.active() == tab(l)
		if !active {
			l.ticking = false
			return nil
		}
		next := tea.Tick(2*time.Second, func(time.Time) tea.Msg { return logsTick{l.gen} })
		if l.follow {
			return tea.Batch(l.poll(), next)
		}
		return next
	}
	return nil
}

func (l *logsTab) key(a *app, msg tea.KeyPressMsg) tea.Cmd {
	if l.filtering {
		switch msg.String() {
		case "enter":
			l.filtering = false
			l.filter.Blur()
			return nil
		case "esc":
			l.filtering = false
			l.filter.Blur()
			l.filter.SetValue("")
			return nil
		}
		var cmd tea.Cmd
		l.filter, cmd = l.filter.Update(msg)
		l.off = -1
		return cmd
	}
	srcs := l.sources(a)
	switch {
	case key.Matches(msg, kUp):
		l.sel = clamp(l.sel-1, 0, len(srcs)-1)
		return l.load(srcs[l.sel])
	case key.Matches(msg, kDown):
		l.sel = clamp(l.sel+1, 0, len(srcs)-1)
		return l.load(srcs[l.sel])
	case key.Matches(msg, kFollow):
		l.follow = !l.follow
		if l.follow {
			l.off = -1
			return l.poll()
		}
	case key.Matches(msg, kPrio):
		l.prio = map[int]int{0: 4, 4: 3, 3: 0}[l.prio]
		return l.load(l.cur)
	case key.Matches(msg, kFind):
		l.filtering = true
		return l.filter.Focus()
	case key.Matches(msg, kPgUp):
		l.scroll(-l.page())
	case key.Matches(msg, kPgDown):
		l.scroll(l.page())
	case key.Matches(msg, kTop):
		l.off = 0
		l.follow = false
	case key.Matches(msg, kBottom):
		l.off = -1
	}
	return nil
}

func (l *logsTab) page() int { return max(1, l.lastH-1) }

func (l *logsTab) wheel(up bool) {
	if up {
		l.scroll(-3)
	} else {
		l.scroll(3)
	}
}

func (l *logsTab) scroll(d int) {
	shown := l.visible()
	h := l.lastH
	top := l.off
	if top < 0 {
		top = max(0, len(shown)-h)
	}
	top = clamp(top+d, 0, max(0, len(shown)-h))
	if top >= len(shown)-h {
		l.off = -1
		return
	}
	l.off = top
	if d < 0 {
		l.follow = false
	}
}

func (l *logsTab) visible() []entry {
	q := strings.ToLower(strings.TrimSpace(l.filter.Value()))
	if q == "" && l.prio == 0 {
		return l.entries
	}
	var out []entry
	for _, e := range l.entries {
		if l.prio > 0 && e.prio > l.prio {
			continue
		}
		if q == "" || strings.Contains(strings.ToLower(e.msg), q) || strings.Contains(strings.ToLower(e.source), q) {
			out = append(out, e)
		}
	}
	return out
}

func (a *app) showLogs(s source) tea.Cmd {
	var lt *logsTab
	idx := 0
	for i, tb := range a.tabs {
		if l, ok := tb.(*logsTab); ok {
			lt, idx = l, i
		}
	}
	if lt == nil {
		return nil
	}
	srcs := lt.sources(a)
	found := -1
	for i, src := range srcs {
		if src.unit == s.unit && src.user == s.user {
			found = i
		}
	}
	if found < 0 {
		lt.extra = append(lt.extra, s)
		srcs = lt.sources(a)
		found = len(srcs) - 1
	}
	lt.sel = found
	lt.loaded = false
	return a.switchTo(idx)
}

func (l *logsTab) view(a *app, w, h int) string {
	t := a.t
	srcs := l.sources(a)
	l.sel = clamp(l.sel, 0, len(srcs)-1)
	lw := clamp(w/5, 16, 26)
	if w < 70 {
		lw = 0
	}
	var left []string
	if lw > 0 {
		left = append(left, t.heading("Source"))
		start := clamp(l.sel-(h-3)/2, 0, max(0, len(srcs)-(h-1)))
		for i := start; i < len(srcs) && len(left) < h; i++ {
			s := srcs[i]
			label := s.label
			if i == l.sel {
				left = append(left, t.accent("▌")+t.bold(clip(label, lw-2)))
			} else {
				left = append(left, " "+t.faint(clip(label, lw-2)))
			}
		}
	}
	rw := w - lw
	if lw > 0 {
		rw -= 3
	}
	right := l.pane(a, rw, h)
	if lw == 0 {
		return right
	}
	sep := make([]string, h)
	for i := range sep {
		sep[i] = " " + t.edge("│") + " "
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, block(padAll(left, lw), h), strings.Join(sep, "\n"), right)
}

func padAll(lines []string, w int) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = fit(l, w)
	}
	return out
}

func (l *logsTab) pane(a *app, w, h int) string {
	t := a.t
	prio := map[int]string{0: "all", 4: "warnings and worse", 3: "errors"}[l.prio]
	state := t.dim("paused")
	if l.follow {
		state = t.tint(lvOK, "● live")
	}
	title := t.bold(l.cur.label)
	if l.cur.unit != "" {
		title += t.dim("  " + l.cur.unit)
	}
	head := spread(title, state+t.dim(" · "+prio), w)
	body := h - 2
	if l.filtering || l.filter.Value() != "" {
		body--
	}
	l.lastH = body
	var lines []string
	shown := l.visible()
	switch {
	case l.err != nil:
		lines = []string{t.tint(lvBad, errText(l.err))}
	case !l.loaded || !l.ready:
		lines = []string{t.dim(a.spin.View() + " reading the journal")}
	case len(shown) == 0:
		lines = []string{t.dim("nothing here")}
	default:
		top := l.off
		if top < 0 {
			top = max(0, len(shown)-body)
		}
		top = clamp(top, 0, max(0, len(shown)-body))
		showSource := l.cur.unit == ""
		for _, e := range shown[top:min(len(shown), top+body)] {
			lines = append(lines, l.row(a, e, w, showSource))
		}
	}
	out := []string{head, t.rule(w)}
	out = append(out, strings.Split(block(lines, body), "\n")...)
	if l.filtering || l.filter.Value() != "" {
		l.filter.SetWidth(max(10, w-4))
		out = append(out, l.filter.View())
	}
	return strings.Join(out, "\n")
}

func (l *logsTab) row(a *app, e entry, w int, showSource bool) string {
	t := a.t
	stamp := e.at.Local().Format("15:04:05")
	if !sameDay(e.at, time.Now()) {
		stamp = e.at.Local().Format("Jan 2 15:04")
	}
	text := e.msg
	switch {
	case e.prio <= 3:
		text = t.tint(lvBad, text)
	case e.prio == 4:
		text = t.tint(lvWarn, text)
	case e.prio >= 7:
		text = t.dim(text)
	default:
		text = t.soft(text)
	}
	src := ""
	if showSource && e.source != "" {
		src = t.faint(clip(e.source, 16)) + " "
	}
	return clip(t.dim(stamp)+" "+src+text, w)
}
