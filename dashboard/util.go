package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func size(n uint64) string {
	if n < 1000 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"kB", "MB", "GB", "TB", "PB"}
	v := float64(n) / 1000
	i := 0
	for v >= 999.5 && i < len(units)-1 {
		v /= 1000
		i++
	}
	switch {
	case v >= 99.5:
		return fmt.Sprintf("%.0f %s", v, units[i])
	case v >= 9.95:
		return fmt.Sprintf("%.1f %s", v, units[i])
	}
	return fmt.Sprintf("%.2f %s", v, units[i])
}

func pair(used, total uint64) string {
	a, b := size(used), size(total)
	ua, ub := a[strings.LastIndex(a, " ")+1:], b[strings.LastIndex(b, " ")+1:]
	if ua == ub {
		return strings.TrimSuffix(a, " "+ua) + " / " + b
	}
	return a + " / " + b
}

func rate(bytesPerSec float64) string {
	if bytesPerSec < 0 {
		bytesPerSec = 0
	}
	return size(uint64(bytesPerSec)) + "/s"
}

func pct(f float64) string {
	return fmt.Sprintf("%.0f%%", math.Max(0, f*100))
}

func span(d time.Duration) string {
	d = d.Abs()
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		if m := int(d.Minutes()) % 60; m > 0 && h < 10 {
			return fmt.Sprintf("%dh %dm", h, m)
		}
		return fmt.Sprintf("%dh", h)
	}
	days := int(d.Hours() / 24)
	if h := int(d.Hours()) % 24; h > 0 && days < 10 {
		return fmt.Sprintf("%dd %dh", days, h)
	}
	return fmt.Sprintf("%dd", days)
}

func elapsed(d time.Duration) string {
	d = d.Round(time.Second)
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	if time.Since(t) < 10*time.Second {
		return "just now"
	}
	return span(time.Since(t)) + " ago"
}

func sameDay(a, b time.Time) bool {
	ya, ma, da := a.Date()
	yb, mb, db := b.Date()
	return ya == yb && ma == mb && da == db
}

func clock(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	now := time.Now()
	t = t.Local()
	switch {
	case sameDay(t, now):
		return "today " + t.Format("15:04")
	case sameDay(t, now.AddDate(0, 0, 1)):
		return "tomorrow " + t.Format("15:04")
	case sameDay(t, now.AddDate(0, 0, -1)):
		return "yesterday " + t.Format("15:04")
	case t.Sub(now).Abs() < 6*24*time.Hour:
		return t.Format("Mon 15:04")
	case t.Year() == now.Year():
		return t.Format("Mon 2 Jan")
	}
	return t.Format("2 Jan 2006")
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	t = t.Local()
	if t.Year() == time.Now().Year() {
		return t.Format("Mon 2 Jan 15:04")
	}
	return t.Format("2 Jan 2006 15:04")
}

func commas(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return "-" + commas(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return commas(n) + " " + many
}

func width(s string) int { return ansi.StringWidth(s) }

func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if width(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", max(0, w-width(s)))
}

func fitRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if width(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return strings.Repeat(" ", max(0, w-width(s))) + s
}

func clip(s string, w int) string {
	if width(s) <= w {
		return s
	}
	return ansi.Truncate(s, max(0, w), "…")
}

func spread(left, right string, w int) string {
	gap := w - width(left) - width(right)
	if gap >= 1 {
		return left + strings.Repeat(" ", gap) + right
	}
	if room := w - width(left) - 2; room >= 8 {
		return left + "  " + fitRight(right, room)
	}
	return clip(left, w)
}

func block(lines []string, h int) string {
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

type scroll struct{ off, page, n int }

func (s *scroll) by(d int) { s.off = clamp(s.off+d, 0, s.n-s.page) }

func (s *scroll) view(body string, h int) string {
	lines := strings.Split(body, "\n")
	s.page, s.n = h, len(lines)
	s.by(0)
	return block(lines[s.off:], h)
}

func (s scroll) overflows() bool { return s.n > s.page }

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return min(max(v, lo), hi)
}

func clean(s string) string {
	s = ansi.Strip(s)
	s = strings.ReplaceAll(s, "\t", "  ")
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r >= 0x7f && r < 0xa0 {
			return -1
		}
		return r
	}, s)
}

func cleanAll(list []string) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = clean(s)
	}
	return out
}
