package main

import (
	"strings"

	"charm.land/huh/v2"
)

const secretsPerPage = 5

type secretKey struct {
	name string
	set  bool
}

type secretGroup struct {
	note string
	keys []secretKey
}

func parseSecrets(b []byte) []secretGroup {
	var out []secretGroup
	for _, l := range lines(b) {
		kind, rest, _ := strings.Cut(l, " ")
		switch kind {
		case "G":
			out = append(out, secretGroup{note: clean(rest)})
		case "K":
			if len(out) == 0 {
				out = append(out, secretGroup{})
			}
			name, state, _ := strings.Cut(rest, " ")
			last := &out[len(out)-1]
			last.keys = append(last.keys, secretKey{name: name, set: state == "set"})
		}
	}
	return out
}

func secretIsSet(groups []secretGroup, name string) bool {
	for _, g := range groups {
		for _, k := range g.keys {
			if k.name == name {
				return k.set
			}
		}
	}
	return false
}

func secretCounts(groups []secretGroup) (set, empty int) {
	for _, g := range groups {
		for _, k := range g.keys {
			if k.set {
				set++
			} else {
				empty++
			}
		}
	}
	return set, empty
}

func secretInput(name string, set bool, val *string) *huh.Input {
	placeholder := "empty — leave it to skip"
	if set {
		placeholder = "set — leave it to keep"
	}
	return huh.NewInput().Title(name).Placeholder(placeholder).EchoMode(huh.EchoModePassword).Value(val)
}

func secretPages(groups []secretGroup, vals map[string]*string) []*huh.Group {
	var pages []*huh.Group
	for _, g := range groups {
		for i := 0; i < len(g.keys); i += secretsPerPage {
			var fields []huh.Field
			for _, k := range g.keys[i:min(i+secretsPerPage, len(g.keys))] {
				vals[k.name] = new(string)
				fields = append(fields, secretInput(k.name, k.set, vals[k.name]))
			}
			pages = append(pages, huh.NewGroup(fields...).Description(g.note))
		}
	}
	return pages
}

func secretBlock(groups []secretGroup, vals map[string]*string) string {
	var b strings.Builder
	for _, g := range groups {
		for _, k := range g.keys {
			if v := strings.TrimSpace(*vals[k.name]); v != "" {
				b.WriteString(k.name + "=" + v + "\n")
			}
		}
	}
	return b.String()
}

func secretPaste(val *string) *huh.Text {
	return huh.NewText().Title("Paste your secrets").
		Description("KEY=value lines, as in a .env file. They show as you paste them, and a key left empty is skipped.").
		CharLimit(0).ShowLineNumbers(false).Lines(12).Value(val)
}
