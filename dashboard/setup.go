package main

import (
	"fmt"
	"net/netip"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

func setupPending() bool {
	return !exists(setupTag) && !exists(firstBootTag)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func onTailnet() bool {
	f := strings.Fields(os.Getenv("SSH_CONNECTION"))
	if len(f) < 3 {
		return false
	}
	addr, err := netip.ParseAddr(f[2])
	if err != nil {
		return false
	}
	return netip.MustParsePrefix("100.64.0.0/10").Contains(addr) ||
		netip.MustParsePrefix("fd7a:115c:a1e0::/48").Contains(addr)
}

func setupArgs(args ...string) []string {
	return asRoot(append([]string{libexec + "/setup"}, args...)...)
}

type setupStatus map[string]string

func (s setupStatus) yes(k string) bool { return s[k] == "yes" }

func (s setupStatus) step() string {
	switch {
	case s == nil:
		return ""
	case !s.yes("PASSWORD"):
		return "password"
	case !s.yes("HOSTNAME_SET"):
		return "hostname"
	case !s.yes("TIMEZONE_SET"):
		return "timezone"
	case !s.yes("FEATURES_SET"):
		return "features"
	case s["TAILSCALE"] != "Running" || s["LOCK_KEY"] != "":
		return "tailscale"
	case s["SITE_REPO"] == "":
		return "site"
	}
	return "connect"
}

type (
	setupLoaded struct {
		st  setupStatus
		err error
	}
	setupDone struct {
		step string
		err  error
	}
	setupTick    struct{}
	setupSecrets struct {
		groups []secretGroup
		err    error
		open   bool
	}
)

type setupModel struct {
	t       theme
	w, h    int
	st      setupStatus
	form    *huh.Form
	onForm  func() tea.Cmd
	busy    string
	problem string
	notice  string
	asked   bool
	checked bool
	done    bool
	tailnet bool
	lanOK   bool
	secrets bool
	groups  []secretGroup
}

func newSetup() *setupModel {
	return &setupModel{t: newTheme(true), tailnet: onTailnet()}
}

func loadSetup() tea.Msg {
	out, err := run(30*time.Second, setupArgs("status")...)
	if err != nil {
		return setupLoaded{err: err}
	}
	st := setupStatus{}
	for line := range strings.Lines(string(out)) {
		if k, v, ok := strings.Cut(strings.TrimRight(line, "\n"), "="); ok {
			st[k] = v
		}
	}
	return setupLoaded{st: st}
}

func runSetup(step, stdin string, timeout time.Duration, args ...string) tea.Cmd {
	return func() tea.Msg {
		_, err := runIn(timeout, stdin, setupArgs(args...)...)
		return setupDone{step: step, err: err}
	}
}

func setupTicker() tea.Cmd {
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return setupTick{} })
}

func (m *setupModel) Init() tea.Cmd {
	return tea.Batch(loadSetup, setupTicker(), tea.RequestBackgroundColor)
}

func (m *setupModel) openForm(g *huh.Group, onOK func() tea.Cmd) tea.Cmd {
	return m.openForms([]*huh.Group{g}, onOK)
}

func (m *setupModel) openForms(gs []*huh.Group, onOK func() tea.Cmd) tea.Cmd {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "leave"))
	f := huh.NewForm(gs...).
		WithTheme(huh.ThemeFunc(func(bool) *huh.Styles { return huhStyles(m.t, false) })).
		WithKeyMap(km).
		WithShowHelp(true).
		WithWidth(clamp(m.w-8, 30, 72))
	first := f.Init()
	if mod, _ := f.Update(tea.WindowSizeMsg{Width: m.w, Height: max(m.h-6, 12)}); mod != nil {
		f = mod.(*huh.Form)
	}
	m.form, m.onForm = f, onOK
	return first
}

func (m *setupModel) start(step, label string, stdin string, timeout time.Duration, args ...string) tea.Cmd {
	m.busy, m.problem, m.notice = label, "", ""
	return runSetup(step, stdin, timeout, args...)
}

func (m *setupModel) askPassword() tea.Cmd {
	pw, again := "", ""
	return m.openForm(huh.NewGroup(
		huh.NewInput().Title("Choose a password").
			Description(fmt.Sprintf("For logging in as %s and for the file share. This replaces the temporary password.", m.st["USER"])).
			EchoMode(huh.EchoModePassword).Value(&pw).
			Validate(func(s string) error {
				if len(s) < 8 {
					return fmt.Errorf("use at least 8 characters")
				}
				return nil
			}),
		huh.NewInput().Title("Again").EchoMode(huh.EchoModePassword).Value(&again).
			Validate(func(s string) error {
				if s != pw {
					return fmt.Errorf("those don't match")
				}
				return nil
			}),
	), func() tea.Cmd {
		return m.start("password", "Setting the password", pw+"\n", 30*time.Second, "password")
	})
}

func (m *setupModel) askHostname() tea.Cmd {
	name := m.st["HOSTNAME"]
	return m.openForm(huh.NewGroup(
		huh.NewInput().Title("Name this machine").
			Description("Its name on your network and on your tailnet.").
			Value(&name),
	), func() tea.Cmd {
		return m.start("hostname", "Setting the name", "", 30*time.Second, "hostname", strings.TrimSpace(name))
	})
}

func (m *setupModel) askTimezone() tea.Cmd {
	zone := m.st["TIMEZONE"]
	return m.openForm(huh.NewGroup(
		huh.NewInput().Title("Choose a time zone").
			Description("The clock this machine's update and backup windows follow. Use its full name, like America/New_York.").
			Value(&zone),
	), func() tea.Cmd {
		return m.start("timezone", "Setting the time zone", "", 30*time.Second, "timezone", strings.TrimSpace(zone))
	})
}

func (m *setupModel) askFeatures() tea.Cmd {
	shield := m.st["SHIELD"] != "no"
	return m.openForm(huh.NewGroup(
		huh.NewConfirm().Title("Turn on the shield?").
			Description("A Worker and WAF rules this machine keeps deployed to your Cloudflare zone, which refuse hosting networks and anything that isn't a current browser or a vouched-for bot. It also lists who got through on the dashboard's Allowed tab.").
			Affirmative("Yes").Negative("No").Value(&shield),
	), func() tea.Cmd {
		answer := "no"
		if shield {
			answer = "yes"
		}
		return m.start("features", "Saving your choice", "", 30*time.Second, "features", answer)
	})
}

func (m *setupModel) askTailscale() tea.Cmd {
	m.asked = true
	authKey := ""
	return m.openForm(huh.NewGroup(
		huh.NewInput().Title("Join your tailnet").
			Description("Paste a Tailscale auth key, or leave this empty to log in with a browser.").
			EchoMode(huh.EchoModePassword).Value(&authKey),
	), func() tea.Cmd {
		return m.start("tailscale", "Starting Tailscale", strings.TrimSpace(authKey)+"\n", 90*time.Second, "tailscale")
	})
}

func (m *setupModel) askSite() tea.Cmd {
	repo := m.st["SITE_REPO"]
	return m.openForm(huh.NewGroup(
		huh.NewInput().Title("Connect your site").
			Description("Its repository's address. Use SSH for a private one, and this machine makes its own key for it, or HTTPS for a public one.").
			Placeholder("git@github.com:you/site.git").Value(&repo),
	), func() tea.Cmd {
		return m.start("site", "Making this machine's key", "", 30*time.Second, "site", strings.TrimSpace(repo))
	})
}

func (m *setupModel) listSecrets(open bool) tea.Cmd {
	return func() tea.Msg {
		out, err := run(30*time.Second, setupArgs("secrets", "list")...)
		return setupSecrets{groups: parseSecrets(out), err: err, open: open}
	}
}

func (m *setupModel) askSecrets() tea.Cmd {
	how := ""
	return m.openForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Add your secrets").
			Description("They stay on this machine and in its encrypted backups, never in your repository.").
			Options(
				huh.NewOption("One group at a time, with what each is for", "groups"),
				huh.NewOption("Paste a whole .env block", "paste"),
				huh.NewOption("Restore them from a backup in R2", "restore"),
				huh.NewOption("Skip them for now", "skip"),
			).Value(&how),
	), func() tea.Cmd {
		m.secrets = true
		switch how {
		case "groups":
			m.busy = "Reading what this machine needs"
			return m.listSecrets(true)
		case "paste":
			return m.askBlock()
		case "restore":
			return m.askRestore()
		}
		return m.listSecrets(false)
	})
}

func (m *setupModel) askGroups() tea.Cmd {
	vals := map[string]*string{}
	return m.openForms(secretPages(m.groups, vals), func() tea.Cmd {
		return m.start("secrets", "Saving your secrets", secretBlock(m.groups, vals), time.Minute, "secrets", "set")
	})
}

func (m *setupModel) askBlock() tea.Cmd {
	block := ""
	return m.openForm(huh.NewGroup(secretPaste(&block)), func() tea.Cmd {
		return m.start("secrets", "Saving your secrets", block, time.Minute, "secrets", "set")
	})
}

func (m *setupModel) askRestore() tea.Cmd {
	groups := []secretGroup{{keys: []secretKey{
		{name: "R2_ACCOUNT_ID"}, {name: "R2_BUCKET"}, {name: "R2_ACCESS_KEY_ID"},
		{name: "R2_SECRET_ACCESS_KEY"}, {name: "RESTIC_PASSWORD"},
	}}}
	vals := map[string]*string{}
	groups[0].note = "What opens the backup. Everything else comes back from it."
	return m.openForms(secretPages(groups, vals), func() tea.Cmd {
		return m.start("restore", "Restoring your secrets", secretBlock(groups, vals), 5*time.Minute, "secrets", "restore")
	})
}

func (m *setupModel) finishForm() tea.Cmd {
	finish := func() tea.Cmd {
		return m.start("finish", "Finishing", "", 3*time.Minute, "finish")
	}
	if !secretIsSet(m.groups, "RESTIC_PASSWORD") {
		return finish()
	}
	saved := false
	return m.openForm(huh.NewGroup(
		huh.NewConfirm().Title("Is RESTIC_PASSWORD in your password manager?").
			Description("With the R2 keys, it's all that brings this machine's secrets back if the pool is lost. Nothing can reissue it.").
			Affirmative("Yes").Negative("Not yet").Value(&saved).
			Validate(func(yes bool) error {
				if !yes {
					return fmt.Errorf("save it first, then choose yes")
				}
				return nil
			}),
	), finish)
}

func (m *setupModel) proceed() tea.Cmd {
	if !m.secrets {
		return m.askSecrets()
	}
	return m.finishForm()
}

func (m *setupModel) nextForm() tea.Cmd {
	if m.form != nil || m.busy != "" || m.done {
		return nil
	}
	switch m.st.step() {
	case "password":
		return m.askPassword()
	case "hostname":
		return m.askHostname()
	case "timezone":
		return m.askTimezone()
	case "features":
		return m.askFeatures()
	case "tailscale":
		if !m.asked && m.st["AUTH_URL"] == "" && !m.st.yes("TS_STARTING") && m.st["TAILSCALE"] != "Running" {
			return m.askTailscale()
		}
	case "site":
		return m.askSite()
	}
	return nil
}

func (m *setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.BackgroundColorMsg:
		m.t = newTheme(msg.IsDark())
	case setupTick:
		if m.done {
			return m, nil
		}
		if m.form != nil || m.busy != "" {
			return m, setupTicker()
		}
		return m, tea.Batch(loadSetup, setupTicker())
	case setupLoaded:
		if msg.err != nil {
			if !setupPending() {
				m.done = true
				return m, nil
			}
			m.problem = errText(msg.err)
			return m, nil
		}
		m.st = msg.st
		return m, m.nextForm()
	case setupSecrets:
		m.busy = ""
		if msg.err != nil {
			m.problem = errText(msg.err)
			return m, nil
		}
		m.groups = msg.groups
		if msg.open {
			return m, m.askGroups()
		}
		return m, nil
	case setupDone:
		m.busy = ""
		if msg.err != nil {
			m.problem = errText(msg.err)
			if msg.step == "secrets" || msg.step == "restore" {
				return m, m.listSecrets(false)
			}
			switch msg.step {
			case "tailscale":
				m.asked = false
			case "check":
				m.checked = false
			}
			if msg.step == "check" || msg.step == "finish" {
				return m, nil
			}
			return m, m.retry(msg.step)
		}
		switch msg.step {
		case "check":
			m.checked = true
			m.notice = "This machine can read the site."
			if m.tailnet {
				return m, m.askSecrets()
			}
		case "secrets", "restore":
			m.notice = "Your secrets are saved."
			return m, m.listSecrets(false)
		case "finish":
			m.done = true
			return m, nil
		}
		return m, loadSetup
	}

	if m.form != nil {
		mod, cmd := m.form.Update(msg)
		if f, ok := mod.(*huh.Form); ok {
			m.form = f
		}
		switch m.form.State {
		case huh.StateCompleted:
			onOK := m.onForm
			m.form, m.onForm = nil, nil
			return m, tea.Batch(cmd, onOK())
		case huh.StateAborted:
			return m, tea.Quit
		}
		return m, cmd
	}

	if k, ok := msg.(tea.KeyPressMsg); ok {
		return m, m.press(k)
	}
	return m, nil
}

func (m *setupModel) retry(step string) tea.Cmd {
	problem := m.problem
	var cmd tea.Cmd
	switch step {
	case "password":
		cmd = m.askPassword()
	case "hostname":
		cmd = m.askHostname()
	case "timezone":
		cmd = m.askTimezone()
	case "features":
		cmd = m.askFeatures()
	case "tailscale":
		cmd = m.askTailscale()
	case "site":
		cmd = m.askSite()
	}
	m.problem = problem
	return cmd
}

func (m *setupModel) press(k tea.KeyPressMsg) tea.Cmd {
	s := k.String()
	if s == "ctrl+c" {
		return tea.Quit
	}
	if m.done {
		return tea.Quit
	}
	if m.busy != "" || m.st.step() != "connect" {
		if s == "q" {
			return tea.Quit
		}
		return nil
	}
	switch s {
	case "q":
		return tea.Quit
	case "e":
		m.checked = false
		return m.askSite()
	case "enter":
		if !m.checked {
			return m.start("check", "Checking the site", "", 2*time.Minute, "check")
		}
		if m.tailnet || m.lanOK {
			return m.proceed()
		}
	case "F":
		if m.checked {
			m.lanOK = true
			return m.proceed()
		}
	case "s":
		if m.checked && (m.tailnet || m.lanOK) {
			return m.askSecrets()
		}
	}
	return nil
}

func (m *setupModel) body() string {
	t := m.t
	if m.done {
		return t.bold("Setup is done.") + "\n\n" +
			"This machine has closed SSH on your network, and is starting your site.\n" +
			"From now on, reach it over your tailnet:\n\n  " +
			t.accent(fmt.Sprintf("ssh %s@%s", m.st["USER"], m.tailnetName())) + "\n\n" +
			t.dim("Press any key to disconnect.")
	}
	if m.st == nil {
		return t.dim("Looking at this machine…")
	}
	switch m.st.step() {
	case "tailscale":
		return m.tailscaleBody()
	case "connect":
		return m.connectBody()
	}
	return ""
}

func (m *setupModel) tailnetName() string {
	if n := m.st["TAILNET_NAME"]; n != "" {
		return n
	}
	return m.st["HOSTNAME"]
}

func (m *setupModel) tailscaleBody() string {
	t := m.t
	switch {
	case m.st["LOCK_KEY"] != "":
		return t.bold("Sign this machine") + "\n\n" +
			"Your tailnet uses Tailnet Lock. On a signing node, run:\n\n  " +
			t.accent("tailscale lock sign "+m.st["LOCK_KEY"]) + "\n\n" +
			t.dim("This screen moves on by itself.")
	case m.st["AUTH_URL"] != "":
		return t.bold("Log in to Tailscale") + "\n\n" +
			"Open this in a browser:\n\n  " + t.accent(m.st["AUTH_URL"]) + "\n\n" +
			t.dim("This screen moves on by itself.")
	}
	return t.dim("Waiting for Tailscale…")
}

func (m *setupModel) connectBody() string {
	t := m.t
	var b strings.Builder
	if !m.checked {
		b.WriteString(t.bold("Let this machine read your site") + "\n\n")
		if key := m.st["PUBLIC_KEY"]; key != "" {
			b.WriteString("Add this key to " + t.soft(m.st["SITE_REPO"]) + " as a read-only deploy key:\n\n  ")
			b.WriteString(t.accent(key) + "\n\n")
		} else {
			b.WriteString(t.soft(m.st["SITE_REPO"]) + " has to be public, because this machine has no key for it.\n\n")
		}
		b.WriteString(t.dim("enter checks it · e changes the address · q leaves"))
		return b.String()
	}
	if m.tailnet || m.lanOK {
		set, empty := secretCounts(m.groups)
		b.WriteString(t.bold("Ready to finish") + "\n\n")
		if m.tailnet {
			b.WriteString("You're connected over your tailnet, so closing SSH on your network is safe.\n\n")
		}
		b.WriteString(fmt.Sprintf("Secrets: %d set, %d empty. The dashboard's System tab changes them later.\n\n", set, empty))
		b.WriteString(t.dim("enter finishes · s changes the secrets · q leaves"))
		return b.String()
	}
	b.WriteString(t.bold("Prove the tailnet works") + "\n\n")
	b.WriteString("SSH on your network closes when setup finishes, so log in over the tailnet first:\n\n  ")
	b.WriteString(t.accent(fmt.Sprintf("ssh %s@%s", m.st["USER"], m.tailnetName())) + "\n\n")
	b.WriteString("Add your secrets and finish from that session, so they reach this machine only over the tailnet. Your password works there too.\n\n")
	b.WriteString(t.dim("F carries on from here anyway, and you'd need a keyboard if the tailnet fails · q leaves"))
	return b.String()
}

func (m *setupModel) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.WindowTitle = "Citrine setup"
	t := m.t
	w := clamp(m.w-4, 30, 76)
	parts := []string{t.wordmark() + "  " + t.dim("setup")}
	if m.st["POOL_USED"] == "yes" && !m.done {
		parts = append(parts, lipgloss.NewStyle().Foreground(t.c.warn).Width(w).Render("This machine's pool already holds data. Finishing setup gives this login access to it."))
	}
	switch {
	case m.form != nil:
		parts = append(parts, m.form.View())
	case m.busy != "":
		parts = append(parts, t.dim(m.busy+"…"))
	default:
		parts = append(parts, lipgloss.NewStyle().Width(w).Render(m.body()))
	}
	if m.notice != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(t.c.ok).Width(w).Render(m.notice))
	}
	if m.problem != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(t.c.bad).Width(w).Render(m.problem))
	}
	v.SetContent(lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(parts, "\n\n")))
	return v
}
