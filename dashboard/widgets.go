package main

import (
	"image/color"
	"math"
	"strings"

	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/x/ansi"
)

func (t theme) gauge(frac float64, w int, c color.Color) string {
	if w <= 0 {
		return ""
	}
	frac = math.Max(0, math.Min(1, frac))
	if t.plain {
		n := int(math.Round(frac * float64(w)))
		return paint(c, strings.Repeat("█", n)) + paint(t.c.edge, strings.Repeat("░", w-n))
	}
	halves := int(math.Round(frac * float64(2*w)))
	full := halves / 2
	rest := w - full
	var b strings.Builder
	b.WriteString(paint(c, strings.Repeat("━", full)))
	if halves%2 == 1 {
		b.WriteString(paint(c, "╸"))
		rest--
	}
	b.WriteString(paint(t.c.edge, strings.Repeat("─", rest)))
	return b.String()
}

func (t theme) usage(frac float64) color.Color {
	switch {
	case frac >= 0.9:
		return t.c.bad
	case frac >= 0.75:
		return t.c.warn
	}
	return t.c.accent
}

var ticks = []rune("▁▂▃▄▅▆▇█")

func (t theme) spark(vals []float64, w int, top float64, c color.Color) string {
	if w <= 0 || t.plain {
		return ""
	}
	if len(vals) > w {
		vals = vals[len(vals)-w:]
	}
	if top <= 0 {
		for _, v := range vals {
			top = math.Max(top, v)
		}
	}
	if top <= 0 {
		top = 1
	}
	var b strings.Builder
	for _, v := range vals {
		i := int(math.Round(math.Max(0, v) / top * float64(len(ticks)-1)))
		b.WriteRune(ticks[clamp(i, 0, len(ticks)-1)])
	}
	return strings.Repeat(" ", w-len(vals)) + paint(c, b.String())
}

func stretch(vals []float64, w int) []float64 {
	if len(vals) == 0 || w <= 0 {
		return vals
	}
	out := make([]float64, w)
	for i := range out {
		out[i] = vals[i*len(vals)/w]
	}
	return out
}

func (t theme) bar(frac float64, w int, c color.Color) string {
	if w <= 0 {
		return ""
	}
	frac = math.Max(0, math.Min(1, frac))
	eighths := int(math.Round(frac * float64(w*8)))
	if frac > 0 && eighths == 0 {
		eighths = 1
	}
	parts := []rune(" ▏▎▍▌▋▊▉")
	s := strings.Repeat("█", eighths/8)
	if r := eighths % 8; r > 0 {
		s += string(parts[r])
	}
	if t.plain {
		s = strings.Repeat("#", (eighths+4)/8)
	}
	return paint(c, s) + strings.Repeat(" ", max(0, w-width(s)))
}

func (t theme) panel(title, body string, w int) string {
	return t.box(title, strings.Split(body, "\n"), w, 0)
}

func (t theme) box(title string, lines []string, w, h int) string {
	if w < 4 {
		return ""
	}
	b := t.border()
	inner := w - 4
	if h > 2 {
		lines = strings.Split(block(lines, h-2), "\n")
	}
	head := ""
	if title != "" {
		head = " " + title + " "
		if width(head) > w-4 {
			head = clip(head, w-4)
		}
	}
	var out strings.Builder
	out.WriteString(t.edge(b.TopLeft+b.Top) + head + t.edge(strings.Repeat(b.Top, max(0, w-3-width(head)))+b.TopRight))
	for _, l := range lines {
		out.WriteString("\n" + t.edge(b.Left) + " " + fit(l, inner) + " " + t.edge(b.Right))
	}
	out.WriteString("\n" + t.edge(b.BottomLeft+strings.Repeat(b.Bottom, w-2)+b.BottomRight))
	return out.String()
}

func (t theme) rule(w int) string {
	if t.plain {
		return t.edge(strings.Repeat("-", max(0, w)))
	}
	return t.edge(strings.Repeat("─", max(0, w)))
}

type col struct {
	title string
	w     int
	right bool
}

func widths(cols []col, total int) []int {
	ws := make([]int, len(cols))
	fixed, flex := 0, -1
	for i, c := range cols {
		ws[i] = c.w
		if c.w == 0 {
			flex = i
		}
		fixed += c.w
	}
	fixed += 2 * (len(cols) - 1)
	room := 0
	if flex >= 0 {
		room = clamp(total/6, 4, 12)
	}
	for over := fixed + room - total; over > 0; over-- {
		widest := -1
		for i, w := range ws {
			if i != flex && w > 3 && (widest < 0 || w > ws[widest]) {
				widest = i
			}
		}
		if widest < 0 {
			break
		}
		ws[widest]--
		fixed--
	}
	if flex >= 0 {
		ws[flex] = max(room, total-fixed)
	}
	return ws
}

func (t theme) row(cols []col, ws []int, cells []string) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		if c.right {
			parts[i] = fitRight(cell, ws[i])
		} else {
			parts[i] = fit(cell, ws[i])
		}
	}
	return strings.Join(parts, "  ")
}

func (t theme) table(cols []col, rows [][]string, sel, w, h int, off *int) []string {
	visible := max(1, h-1)
	scrolls := off != nil && len(rows) > visible
	if scrolls {
		w -= 2
	}
	ws := widths(cols, w-2)
	titles := make([]string, len(cols))
	for i, c := range cols {
		titles[i] = t.heading(c.title)
	}
	out := []string{"  " + t.row(cols, ws, titles)}
	if off != nil {
		if sel >= 0 {
			if sel < *off {
				*off = sel
			}
			if sel >= *off+visible {
				*off = sel - visible + 1
			}
		}
		*off = clamp(*off, 0, max(0, len(rows)-visible))
	}
	start := 0
	if off != nil {
		start = *off
	}
	for i := start; i < len(rows) && i < start+visible; i++ {
		lead := "  "
		if i == sel {
			lead = t.accent("▌") + " "
		}
		out = append(out, lead+t.row(cols, ws, rows[i]))
	}
	if scrolls {
		t.scrollbar(out[1:], start, len(rows), w)
	}
	return out
}

func (t theme) scrollbar(lines []string, off, total, w int) {
	bar := t.thumb(len(lines), off, total)
	for i, l := range lines {
		lines[i] = fit(l, w) + " " + bar[i]
	}
}

func (t theme) thumb(h, off, total int) []string {
	out := make([]string, max(0, h))
	track, grip := "│", "┃"
	if t.plain {
		grip = "█"
	}
	if h <= 0 || total <= h {
		for i := range out {
			out[i] = " "
		}
		return out
	}
	size := clamp(h*h/total, 1, h)
	at := (h - size) * clamp(off, 0, total-h) / (total - h)
	for i := range out {
		if i >= at && i < at+size {
			out[i] = paint(t.c.muted, grip)
		} else {
			out[i] = t.edge(track)
		}
	}
	return out
}

func (t theme) keyval(k, v string, kw int) string {
	return fit(t.dim(k), kw) + v
}

func (t theme) wrap(lead string, c color.Color, s string, w int) []string {
	lines := strings.Split(ansi.Wrap(s, max(8, w-width(lead)), ""), "\n")
	for i, l := range lines {
		lines[i] = lead + paint(c, strings.TrimSpace(l))
	}
	return lines
}

func (t theme) wrapKV(k, v string, kw, w int) []string {
	lines := strings.Split(ansi.Wrap(v, max(8, w-kw), ""), "\n")
	out := []string{t.keyval(k, lines[0], kw)}
	for _, l := range lines[1:] {
		out = append(out, strings.Repeat(" ", kw)+strings.TrimLeft(l, " "))
	}
	return out
}

func (t theme) chart(items []count, w int, c color.Color) []string {
	if len(items) == 0 {
		return []string{t.dim("nothing yet")}
	}
	top := 0
	for _, it := range items {
		top = max(top, it.n)
	}
	nw := 0
	for _, it := range items {
		nw = max(nw, width(commas(it.n)))
	}
	label := clamp(w/3, 8, 28)
	barW := max(4, w-label-nw-2)
	var out []string
	for _, it := range items {
		out = append(out, fit(t.soft(it.label), label)+" "+t.bar(float64(it.n)/float64(max(1, top)), barW, c)+" "+fitRight(t.text(commas(it.n)), nw))
	}
	return out
}

type count struct {
	label string
	n     int
}

type spring struct {
	pos, vel, target float64
}

var motion = harmonica.NewSpring(harmonica.FPS(fps), 5.0, 0.85)

const fps = 30

func (s *spring) set(v float64) {
	if math.IsNaN(v) {
		v = 0
	}
	s.target = v
}

func (s *spring) step() bool {
	if s.settled() {
		s.pos, s.vel = s.target, 0
		return false
	}
	s.pos, s.vel = motion.Update(s.pos, s.vel, s.target)
	return true
}

func (s *spring) settled() bool {
	return math.Abs(s.pos-s.target) < 0.001 && math.Abs(s.vel) < 0.001
}
