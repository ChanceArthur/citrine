package main

import (
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var windows = []struct {
	d    time.Duration
	name string
}{
	{time.Hour, "1h"},
	{24 * time.Hour, "24h"},
	{7 * 24 * time.Hour, "7d"},
	{30 * 24 * time.Hour, "30d"},
}

var kWindow = bind("w", "window", "w")

func (a *app) visitWindow() time.Duration { return windows[a.visitIdx].d }

func (a *app) cycleWindow() tea.Cmd {
	a.visitIdx = (a.visitIdx + 1) % len(windows)
	a.scrolls[a.cur].off = 0
	a.s.traffic = result[traffic]{}
	a.s.stops = result[[]shieldEvent]{}
	a.s.visits = result[visits]{}
	return a.reload(kTraffic, kStops, kVisits)
}

func (a *app) windowPicker() string {
	var picker []string
	for i, win := range windows {
		if i == a.visitIdx {
			picker = append(picker, a.t.accent("["+win.name+"]"))
		} else {
			picker = append(picker, a.t.dim(" "+win.name+" "))
		}
	}
	return a.t.faint("window ") + strings.Join(picker, " ")
}

func (a *app) trafficSpark(v traffic, hist []float64, iw int, c color.Color) []string {
	if v.requests == 0 {
		return nil
	}
	return []string{a.t.spark(stretch(hist, iw), iw, 0, c), a.t.dim(spread(windows[a.visitIdx].name+" ago", "now", iw))}
}

type chart struct {
	title string
	items []count
	c     color.Color
}

func (a *app) charts(charts []chart, w int) []string {
	t := a.t
	cols := 1
	switch {
	case w >= 120:
		cols = 3
	case w >= 76:
		cols = 2
	}
	bw := (w - 2*(cols-1)) / cols
	var out []string
	for i := 0; i < len(charts); i += cols {
		var row []string
		for j := i; j < min(i+cols, len(charts)); j++ {
			cw := bw
			if j == i+cols-1 {
				cw = w - (bw+2)*(cols-1)
			}
			ch := charts[j]
			if j > i {
				row = append(row, "  ")
			}
			row = append(row, t.box(t.bold(ch.title), t.chart(ch.items, cw-4, ch.c), cw, 7))
		}
		out = append(out, lipgloss.JoinHorizontal(lipgloss.Top, row...))
	}
	return out
}

func (a *app) listWithDetail(title string, list []string, detailTitle string, detail []string, w int) string {
	t := a.t
	if w >= 110 {
		lw := (w - 2) * 3 / 5
		rows := max(len(list), len(detail)) + 2
		return lipgloss.JoinHorizontal(lipgloss.Top,
			t.box(title, list, lw, rows), "  ",
			t.box(detailTitle, detail, w-lw-2, rows))
	}
	return t.box(title, list, w, 0) + "\n" + t.box(detailTitle, detail, w, 0)
}

func fieldWidth(w int) int { return clamp(w/3, 10, 22) }

func field(t theme, name, value string, w int) string {
	if value == "" {
		value = "—"
	}
	return fit(t.dim(name), fieldWidth(w)) + " " + t.soft(value)
}

func wrappedField(t theme, name, value string, w int) []string {
	kw := fieldWidth(w) + 1
	lines := strings.Split(ansi.Wrap(value, max(1, w-kw), ""), "\n")
	out := []string{field(t, name, lines[0], w)}
	for _, l := range lines[1:] {
		out = append(out, strings.Repeat(" ", kw)+t.soft(l))
	}
	return out
}

func share(part, whole float64) float64 {
	if whole <= 0 {
		return 0
	}
	return part / whole
}
