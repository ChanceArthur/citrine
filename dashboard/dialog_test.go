package main

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

func pump(a *app, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	got := make(chan tea.Msg, 1)
	go func() { got <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-got:
	case <-time.After(50 * time.Millisecond):
		return
	}
	switch m := msg.(type) {
	case tea.BatchMsg:
		for _, c := range m {
			pump(a, c)
		}
	case nil:
	default:
		if a.dlg != nil {
			pump(a, a.updateDialog(m))
		}
	}
}

func TestDialogOpenedFromOnOKStaysOpen(t *testing.T) {
	a := newApp(config{}, false)
	a.w, a.h = 100, 40
	picked, val := "", ""
	pump(a, a.choose("Which secret?", "", []huh.Option[string]{huh.NewOption("GH_TOKEN", "GH_TOKEN")}, func(name string) tea.Cmd {
		picked = name
		return a.open(huh.NewGroup(huh.NewInput().Title(name).Value(&val)), false, func() tea.Cmd { return nil })
	}))

	pump(a, a.updateDialog(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if picked != "GH_TOKEN" {
		t.Fatalf("choose didn't complete: picked %q", picked)
	}
	if a.dlg == nil {
		t.Fatal("the dialog opened from onOK was dropped")
	}

	pump(a, a.updateDialog(tea.PasteMsg{Content: "ghp_new"}))
	pump(a, a.updateDialog(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if val != "ghp_new" || a.dlg != nil {
		t.Fatalf("second dialog: val %q, still open %v", val, a.dlg != nil)
	}
}
