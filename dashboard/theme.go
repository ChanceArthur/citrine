package main

import (
	"image/color"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
)

type level int

const (
	lvNone level = iota
	lvOK
	lvInfo
	lvWarn
	lvBad
)

func worst(ls ...level) level {
	w := lvNone
	for _, l := range ls {
		w = max(w, l)
	}
	return w
}

type colors struct {
	text   color.Color
	soft   color.Color
	muted  color.Color
	faint  color.Color
	dim    color.Color
	edge   color.Color
	raised color.Color
	ink    color.Color
	accent color.Color
	flame  color.Color
	brand  []color.Color
	ok     color.Color
	info   color.Color
	warn   color.Color
	bad    color.Color
}

type theme struct {
	c     colors
	dark  bool
	plain bool
}

func newTheme(dark bool) theme {
	ld := lipgloss.LightDark(dark)
	tone := func(c color.Color) color.Color { return ld(lipgloss.Darken(c, 0.35), c) }
	gold, orange := lipgloss.Color("#F9C23C"), lipgloss.Color("#F57C1F")
	return theme{
		dark:  dark,
		plain: os.Getenv("TERM") == "linux",
		c: colors{
			text:   ld(charmtone.Pepper, charmtone.Salt),
			soft:   ld(charmtone.BBQ, charmtone.Sash),
			muted:  ld(charmtone.Char, charmtone.Steep),
			faint:  ld(charmtone.Iron, charmtone.Steam),
			dim:    charmtone.Squid,
			edge:   ld(charmtone.Steep, charmtone.Char),
			raised: ld(charmtone.Sash, charmtone.BBQ),
			ink:    ld(charmtone.Salt, charmtone.Pepper),
			accent: tone(gold),
			flame:  tone(orange),
			brand:  []color.Color{tone(orange), tone(gold)},
			ok:     tone(charmtone.Guac),
			info:   tone(charmtone.Malibu),
			warn:   tone(charmtone.Tang),
			bad:    tone(charmtone.Coral),
		},
	}
}

func paint(c color.Color, s string) string {
	if s == "" {
		return ""
	}
	return lipgloss.NewStyle().Foreground(c).Render(s)
}

func (t theme) text(s string) string   { return paint(t.c.text, s) }
func (t theme) soft(s string) string   { return paint(t.c.soft, s) }
func (t theme) muted(s string) string  { return paint(t.c.muted, s) }
func (t theme) faint(s string) string  { return paint(t.c.faint, s) }
func (t theme) dim(s string) string    { return paint(t.c.dim, s) }
func (t theme) edge(s string) string   { return paint(t.c.edge, s) }
func (t theme) accent(s string) string { return paint(t.c.accent, s) }

func (t theme) bold(s string) string {
	return lipgloss.NewStyle().Foreground(t.c.text).Bold(true).Render(s)
}

func (t theme) color(l level) color.Color {
	switch l {
	case lvOK:
		return t.c.ok
	case lvInfo:
		return t.c.info
	case lvWarn:
		return t.c.warn
	case lvBad:
		return t.c.bad
	}
	return t.c.dim
}

func (t theme) tint(l level, s string) string { return paint(t.color(l), s) }

func (t theme) dot(l level) string {
	if l == lvNone {
		return paint(t.c.dim, "○")
	}
	return paint(t.color(l), "●")
}

func (t theme) check(ok bool) string {
	if ok {
		return paint(t.c.ok, "✓")
	}
	return paint(t.c.bad, "✗")
}

func (t theme) heading(s string) string {
	return lipgloss.NewStyle().Foreground(t.c.faint).Bold(true).Render(strings.ToUpper(s))
}

func (t theme) tag(l level, s string) string {
	return lipgloss.NewStyle().Foreground(t.color(l)).Bold(true).Render(s)
}

func (t theme) wordmark() string {
	word := "Citrine"
	blend := lipgloss.Blend1D(len(word), t.c.brand...)
	var b strings.Builder
	for i, r := range word {
		b.WriteString(lipgloss.NewStyle().Foreground(blend[i]).Bold(true).Render(string(r)))
	}
	return b.String()
}

func (t theme) border() lipgloss.Border {
	if t.plain {
		return lipgloss.NormalBorder()
	}
	return lipgloss.RoundedBorder()
}
