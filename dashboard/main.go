package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
)

func main() {
	login := flag.Bool("login", false, "start only for the pool user's interactive login, and say nothing otherwise")
	setup := flag.Bool("setup", false, "run the setup screen, and nothing else")
	tail := flag.Bool("visits", false, "write the site's visits to stdout, from the shield Worker's tail, and nothing else")
	flag.Parse()

	cfg := loadConfig()
	if *tail {
		os.Exit(runVisits(cfg))
	}
	if *login && (cfg.uid() != os.Getuid() || os.Getenv("TERM") == "dumb") {
		os.Exit(2)
	}
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		fmt.Fprintln(os.Stderr, "dashboard: needs a terminal — try ssh -t")
		os.Exit(2)
	}
	prepareEnv(cfg["TIMEZONE"])

	if *setup && !setupPending() {
		fmt.Fprintln(os.Stderr, "dashboard: setup has already finished")
		os.Exit(1)
	}
	if setupPending() {
		if _, err := tea.NewProgram(newSetup()).Run(); err != nil {
			fmt.Fprintln(os.Stderr, "dashboard:", err)
			os.Exit(1)
		}
		return
	}

	m, err := tea.NewProgram(newApp(cfg, *login)).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "dashboard:", err)
		os.Exit(1)
	}
	if a, ok := m.(*app); ok && a.last != nil {
		os.Exit(a.runLast())
	}
}

func prepareEnv(tz string) {
	if loc, err := time.LoadLocation(tz); tz != "" && err == nil {
		time.Local = loc
		os.Setenv("TZ", tz)
	}
	uid := strconv.Itoa(os.Getuid())
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		os.Setenv("XDG_RUNTIME_DIR", "/run/user/"+uid)
	}
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		os.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/"+uid+"/bus")
	}
	os.Setenv("CITRINE_DASHBOARD", "1")
	os.Setenv("SYSTEMD_COLORS", "0")
	os.Setenv("SYSTEMD_PAGER", "")
}
