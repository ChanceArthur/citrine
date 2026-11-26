package main

import "charm.land/bubbles/v2/key"

func bind(help, desc string, keys ...string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(help, desc))
}

var (
	kQuit    = bind("q", "quit", "q", "ctrl+c")
	kHelp    = bind("?", "help", "?")
	kShell   = bind("!", "shell", "!")
	kTasks   = bind("t", "tasks", "t")
	kRefresh = bind("ctrl+r", "refresh", "ctrl+r", "f5")
	kNext    = bind("→/tab", "next tab", "right", "l", "tab")
	kPrev    = bind("←/⇧tab", "prev tab", "left", "h", "shift+tab")
	kUp      = bind("↑/k", "up", "up", "k")
	kDown    = bind("↓/j", "down", "down", "j")
	kPgUp    = bind("pgup", "page up", "pgup", "ctrl+u")
	kPgDown  = bind("pgdn", "page down", "pgdown", "ctrl+d")
	kTop     = bind("g", "top", "home", "g")
	kBottom  = bind("G", "bottom", "end", "G")
	kBack    = bind("esc", "close", "esc")
	kUnlock  = bind("U", "unlock sudo", "U")
	kSelect  = bind("↑↓", "select", "up", "down", "k", "j")
)
