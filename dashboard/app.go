package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

type tab interface {
	title() string
	needs() []kind
	keys(a *app) []key.Binding
	key(a *app, msg tea.KeyPressMsg) tea.Cmd
	view(a *app, w, h int) string
}

type capturer interface {
	capturing() bool
}

type activator interface {
	activate(a *app) tea.Cmd
}

type selector interface {
	selects(msg tea.KeyPressMsg) bool
}

type wheeler interface {
	wheel(up bool)
}

type overlay int

const (
	noOverlay overlay = iota
	helpOverlay
	tasksOverlay
)

type toast struct {
	lv   level
	text string
	at   time.Time
}

type dialog struct {
	form *huh.Form
	onOK func() tea.Cmd
	w    int
}

type (
	tickMsg   time.Time
	frameMsg  struct{}
	shellDone struct {
		err  error
		into string
	}
	unlocked struct {
		err  error
		then tea.Cmd
	}
)

type app struct {
	ctx      context.Context
	cancel   context.CancelFunc
	cfg      config
	login    bool
	t        theme
	w, h     int
	tabs     []tab
	scrolls  []scroll
	cur      int
	meta     [kinds]sched
	s        store
	tasks    []*task
	nextID   int
	toast    toast
	dlg      *dialog
	over     overlay
	taskSel  int
	taskOff  int
	spin     spinner.Model
	help     help.Model
	springs  map[string]*spring
	moving   bool
	spinning bool
	tabHits  [][2]int
	tabRow   int
	host     string
	rel      release
	visitIdx int
	hooks    map[kind]func() tea.Cmd
	last     *spec
}

func newApp(cfg config, login bool) *app {
	ctx, cancel := context.WithCancel(context.Background())
	host, _ := os.Hostname()
	a := &app{
		ctx:      ctx,
		cancel:   cancel,
		cfg:      cfg,
		login:    login,
		t:        newTheme(true),
		springs:  map[string]*spring{},
		hooks:    map[kind]func() tea.Cmd{},
		host:     host,
		rel:      readRelease(releasePath),
		visitIdx: 1,
	}
	a.tabs = []tab{
		&overviewTab{},
		&containersTab{},
		&servicesTab{},
		&storageTab{},
		&backupsTab{},
		&systemTab{},
		&networkTab{},
	}
	if cfg["SHIELD"] == "yes" {
		a.tabs = append(a.tabs, &allowedTab{})
	}
	a.tabs = append(a.tabs, &blockedTab{}, newLogsTab())
	a.scrolls = make([]scroll, len(a.tabs))
	a.spin = spinner.New(spinner.WithSpinner(spinner.MiniDot))
	a.help = help.New()
	a.restyle()
	return a
}

func (a *app) restyle() {
	a.spin.Style = lipgloss.NewStyle().Foreground(a.t.c.flame)
	a.help.Styles = help.DefaultStyles(a.t.dark)
	a.help.Styles.ShortKey = lipgloss.NewStyle().Foreground(a.t.c.muted)
	a.help.Styles.ShortDesc = lipgloss.NewStyle().Foreground(a.t.c.dim)
	a.help.Styles.ShortSeparator = lipgloss.NewStyle().Foreground(a.t.c.edge)
	a.help.Styles.Ellipsis = lipgloss.NewStyle().Foreground(a.t.c.edge)
	a.help.ShortSeparator = " · "
}

func (a *app) Init() tea.Cmd {
	a.meta[kSudo].busy = true
	a.spinning = true
	return tea.Batch(
		tea.RequestBackgroundColor,
		a.spin.Tick,
		a.load(kSudo),
		a.schedule(false),
		tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
	)
}

func (a *app) active() tab { return a.tabs[a.cur] }

func (a *app) wanted() []kind {
	seen := map[kind]bool{kSys: true, kZone: true}
	out := []kind{kSys, kZone}
	for _, k := range a.active().needs() {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	if a.scrubbing() && !seen[kScrub] {
		out = append(out, kScrub)
	}
	return out
}

func (a *app) scrubbing() bool {
	u := a.unit("citrine-scrub.service")
	return u.active == "activating"
}

func (a *app) schedule(force bool) tea.Cmd {
	var cmds []tea.Cmd
	for _, k := range a.wanted() {
		m := &a.meta[k]
		if m.busy {
			continue
		}
		interval := every[k]
		if k == kScrub && a.scrubbing() {
			interval = 5 * time.Second
		}
		if !force && !m.at.IsZero() && (interval == 0 || time.Since(m.at) < interval) {
			continue
		}
		if cmd := a.load(k); cmd != nil {
			m.busy = true
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

func (a *app) reload(ks ...kind) tea.Cmd {
	var cmds []tea.Cmd
	for _, k := range ks {
		if a.meta[k].busy {
			continue
		}
		if cmd := a.load(k); cmd != nil {
			a.meta[k].busy = true
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

func (a *app) loading(k kind) bool { return a.meta[k].busy }

func (a *app) busy() bool {
	for _, m := range a.meta {
		if m.busy {
			return true
		}
	}
	return len(a.running()) > 0 || a.scrubbing()
}

func (a *app) spinUp() tea.Cmd {
	if a.spinning || !a.busy() {
		return nil
	}
	a.spinning = true
	return a.spin.Tick
}

func (a *app) animate(name string, v float64) tea.Cmd {
	sp := a.springs[name]
	if sp == nil {
		sp = &spring{}
		a.springs[name] = sp
	}
	sp.set(v)
	if a.moving {
		return nil
	}
	a.moving = true
	return tea.Tick(time.Second/fps, func(time.Time) tea.Msg { return frameMsg{} })
}

func (a *app) shown(name string) float64 {
	if sp := a.springs[name]; sp != nil {
		return sp.pos
	}
	return 0
}

func (a *app) notify(lv level, text string) {
	a.toast = toast{lv: lv, text: text, at: time.Now()}
}

func (a *app) running() []*task {
	var out []*task
	for _, t := range a.tasks {
		if t.running {
			out = append(out, t)
		}
	}
	return out
}

func (a *app) taskByID(id int) *task {
	for _, t := range a.tasks {
		if t.id == id {
			return t
		}
	}
	return nil
}

func (a *app) start(s spec) tea.Cmd {
	for _, t := range a.tasks {
		if t.running && t.spec.key == s.key {
			a.notify(lvWarn, s.title+" is already running")
			return nil
		}
	}
	if !s.root || a.s.sudo == sudoFree {
		return a.launch(s)
	}
	return func() tea.Msg {
		_, err := run(5*time.Second, "sudo", "-n", "-v")
		return sudoChecked{s: s, err: err}
	}
}

func (a *app) launch(s spec) tea.Cmd {
	if s.last {
		a.last = &s
		return a.leave()
	}
	a.nextID++
	t := &task{id: a.nextID, spec: s, started: time.Now(), running: true, ch: make(chan tea.Msg, 256)}
	a.tasks = append(a.tasks, t)
	a.taskSel = len(a.tasks) - 1
	detach := !s.root || a.s.sudo == sudoFree
	go execute(a.ctx, t.id, s, detach, t.ch)
	a.notify(lvInfo, s.title+" started")
	return tea.Batch(listen(t.ch), a.spinUp())
}

type startMsg struct{ s spec }

type sudoChecked struct {
	s   spec
	err error
}

func (a *app) askPassword(problem string, then tea.Cmd) tea.Cmd {
	pw := ""
	desc := "Actions that change the machine run as root."
	if problem != "" {
		desc = problem
	}
	field := huh.NewInput().Title("Password for sudo").Description(desc).EchoMode(huh.EchoModePassword).Value(&pw)
	return a.open(huh.NewGroup(field), false, func() tea.Cmd {
		return func() tea.Msg {
			_, err := runIn(30*time.Second, pw+"\n", "sudo", "-S", "-p", "", "-v")
			return unlocked{err: err, then: then}
		}
	})
}

func (a *app) confirm(title, body, yes string, danger bool, then tea.Cmd) tea.Cmd {
	ok := false
	field := huh.NewConfirm().Title(title).Description(body).Affirmative(yes).Negative("Cancel").Value(&ok)
	return a.open(huh.NewGroup(field), danger, func() tea.Cmd {
		if ok {
			return then
		}
		return nil
	})
}

func (a *app) typed(title, body, word string, then tea.Cmd) tea.Cmd {
	val := ""
	field := huh.NewInput().Title(title).Description(body).Placeholder(word).Value(&val).
		Validate(func(s string) error {
			if strings.TrimSpace(s) != word {
				return fmt.Errorf("type %s to go ahead", word)
			}
			return nil
		})
	return a.open(huh.NewGroup(field), true, func() tea.Cmd { return then })
}

func (a *app) choose(title, body string, opts []huh.Option[string], then func(string) tea.Cmd) tea.Cmd {
	val := ""
	sel := huh.NewSelect[string]().Title(title).Description(body).Options(opts...).Value(&val).Height(min(len(opts)+2, 12))
	return a.open(huh.NewGroup(sel), false, func() tea.Cmd { return then(val) })
}

func (a *app) open(g *huh.Group, danger bool, onOK func() tea.Cmd) tea.Cmd {
	w := clamp(a.w-8, 30, 64)
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("esc", "ctrl+c"), key.WithHelp("esc", "cancel"))
	f := huh.NewForm(g).
		WithTheme(huh.ThemeFunc(func(bool) *huh.Styles { return a.huhStyles(danger) })).
		WithKeyMap(km).
		WithShowHelp(true).
		WithWidth(w - 4)
	first := f.Init()
	if m, _ := f.Update(tea.WindowSizeMsg{Width: a.w, Height: a.h}); m != nil {
		f = m.(*huh.Form)
	}
	a.dlg = &dialog{form: f, onOK: onOK, w: w}
	return first
}

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = msg.Width, msg.Height
		if a.dlg != nil {
			return a, a.updateDialog(msg)
		}
		return a, nil

	case tea.BackgroundColorMsg:
		a.t = newTheme(msg.IsDark())
		a.restyle()
		return a, nil

	case tickMsg:
		return a, tea.Batch(a.schedule(false), a.spinUp(), tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }))

	case frameMsg:
		busy := false
		for _, sp := range a.springs {
			if sp.step() {
				busy = true
			}
		}
		if !busy {
			a.moving = false
			return a, nil
		}
		return a, tea.Tick(time.Second/fps, func(time.Time) tea.Msg { return frameMsg{} })

	case loaded:
		a.meta[msg.k].busy = false
		a.meta[msg.k].at = time.Now()
		if msg.locked && a.s.sudo != sudoFree {
			a.s.sudo = sudoLocked
		}
		cmd := msg.apply(a)
		if hook, ok := a.hooks[msg.k]; ok {
			delete(a.hooks, msg.k)
			cmd = tea.Batch(cmd, hook())
		}
		return a, cmd

	case logsLoaded, logsTick:
		for _, tb := range a.tabs {
			if l, ok := tb.(*logsTab); ok {
				return a, l.handle(a, msg)
			}
		}
		return a, nil

	case spinner.TickMsg:
		if !a.busy() {
			a.spinning = false
			return a, nil
		}
		var cmd tea.Cmd
		a.spin, cmd = a.spin.Update(msg)
		return a, cmd

	case taskLine:
		if t := a.taskByID(msg.id); t != nil {
			t.lines = append(t.lines, msg.line)
			if len(t.lines) > 4000 {
				t.lines = t.lines[len(t.lines)-4000:]
			}
			return a, listen(t.ch)
		}
		return a, nil

	case taskEnded:
		t := a.taskByID(msg.id)
		if t == nil {
			return a, nil
		}
		t.running, t.ended, t.err = false, time.Now(), msg.err
		if msg.err != nil {
			a.notify(lvBad, t.spec.title+" failed — t shows its log")
		} else {
			done := t.spec.done
			if done == "" {
				done = t.spec.title + " finished"
			}
			a.notify(lvOK, done+" · "+span(t.took()))
		}
		return a, a.reload(append(t.spec.after, kUnits, kUserUnits, kFailed)...)

	case appPreview:
		return a, containersTab{}.confirmApp(a, msg)
	case secretsListed:
		return a, systemTab{}.chooseSecret(a, msg)
	case startMsg:
		return a, a.start(msg.s)

	case sudoChecked:
		switch {
		case msg.err == nil:
			return a, a.launch(msg.s)
		case errors.Is(msg.err, errPassword):
			return a, a.askPassword("", func() tea.Msg { return startMsg{msg.s} })
		}
		a.notify(lvBad, msg.err.Error())
		return a, nil

	case unlocked:
		if msg.err != nil {
			return a, a.askPassword(msg.err.Error(), msg.then)
		}
		if a.s.sudo == sudoLocked {
			a.s.sudo = sudoAsks
		}
		return a, tea.Batch(msg.then, a.schedule(true))

	case shellDone:
		if msg.into != "" && msg.err != nil {
			var exit *exec.ExitError
			if errors.As(msg.err, &exit) && (exit.ExitCode() == 126 || exit.ExitCode() == 127) {
				a.notify(lvBad, msg.into+" has no shell")
			} else {
				a.notify(lvBad, "couldn't open a shell in "+msg.into+": "+errText(msg.err))
			}
		}
		return a, a.schedule(true)

	case tea.MouseWheelMsg:
		if a.dlg != nil || a.over != noOverlay {
			return a, nil
		}
		up := msg.Button == tea.MouseWheelUp
		if wh, ok := a.active().(wheeler); ok {
			wh.wheel(up)
			return a, nil
		}
		code := tea.KeyDown
		if up {
			code = tea.KeyUp
		}
		return a, a.tabKey(tea.KeyPressMsg{Code: code})

	case tea.MouseClickMsg:
		if a.dlg != nil || a.over != noOverlay || msg.Button != tea.MouseLeft {
			return a, nil
		}
		if msg.Y == a.tabRow {
			for i, hit := range a.tabHits {
				if msg.X >= hit[0] && msg.X < hit[1] {
					return a, a.switchTo(i)
				}
			}
		}
		return a, nil

	case tea.KeyPressMsg:
		return a, a.keyPress(msg)
	}

	if a.dlg != nil {
		return a, a.updateDialog(msg)
	}
	return a, nil
}

func (a *app) updateDialog(msg tea.Msg) tea.Cmd {
	m, cmd := a.dlg.form.Update(msg)
	if f, ok := m.(*huh.Form); ok {
		a.dlg.form = f
	}
	switch a.dlg.form.State {
	case huh.StateCompleted:
		onOK := a.dlg.onOK
		a.dlg = nil
		return tea.Batch(cmd, onOK())
	case huh.StateAborted:
		a.dlg = nil
		return nil
	}
	return cmd
}

func (a *app) switchTo(i int) tea.Cmd {
	a.cur = (i + len(a.tabs)) % len(a.tabs)
	cmd := a.schedule(false)
	if act, ok := a.active().(activator); ok {
		cmd = tea.Batch(cmd, act.activate(a))
	}
	return cmd
}

func (a *app) onLoad(k kind, fn func() tea.Cmd) {
	a.hooks[k] = fn
}

func (a *app) keyPress(msg tea.KeyPressMsg) tea.Cmd {
	if a.dlg != nil {
		return a.updateDialog(msg)
	}
	if a.over != noOverlay {
		return a.overlayKey(msg)
	}
	if c, ok := a.active().(capturer); ok && c.capturing() {
		return a.active().key(a, msg)
	}
	switch {
	case key.Matches(msg, kQuit):
		return a.quit()
	case key.Matches(msg, kHelp):
		a.over = helpOverlay
		return nil
	case key.Matches(msg, kTasks):
		if len(a.tasks) == 0 {
			a.notify(lvNone, "nothing has run yet this session")
			return nil
		}
		a.over = tasksOverlay
		a.taskOff = -1
		return nil
	case key.Matches(msg, kShell):
		return a.shell()
	case key.Matches(msg, kRefresh):
		a.notify(lvNone, "refreshing")
		return a.schedule(true)
	case key.Matches(msg, kUnlock):
		if a.s.sudo != sudoFree {
			return a.askPassword("", nil)
		}
		return nil
	case key.Matches(msg, kNext):
		return a.switchTo(a.cur + 1)
	case key.Matches(msg, kPrev):
		return a.switchTo(a.cur - 1)
	}
	if s := msg.String(); len(s) == 1 {
		if i := strings.IndexByte("1234567890", s[0]); i >= 0 && i < len(a.tabs) {
			return a.switchTo(i)
		}
	}
	return a.tabKey(msg)
}

func (a *app) tabKey(msg tea.KeyPressMsg) tea.Cmd {
	s := &a.scrolls[a.cur]
	if sel, ok := a.active().(selector); !s.overflows() || ok && sel.selects(msg) {
		return a.active().key(a, msg)
	}
	switch {
	case key.Matches(msg, kUp):
		s.by(-1)
	case key.Matches(msg, kDown):
		s.by(1)
	case key.Matches(msg, kPgUp):
		s.by(-s.page)
	case key.Matches(msg, kPgDown):
		s.by(s.page)
	case key.Matches(msg, kTop):
		s.by(-s.n)
	case key.Matches(msg, kBottom):
		s.by(s.n)
	default:
		return a.active().key(a, msg)
	}
	return nil
}

func (a *app) quitKey() key.Binding {
	if a.login {
		return bind("q", "log out", "q", "ctrl+c")
	}
	return kQuit
}

func (a *app) quit() tea.Cmd {
	if n := len(a.running()); n > 0 {
		word := "it carries"
		if n > 1 {
			word = "they carry"
		}
		title, yes := "Leave the dashboard?", "Leave"
		if a.login {
			title, yes = "Log out?", "Log out"
		}
		return a.confirm(title, plural(n, "task is", "tasks are")+" still running — "+word+" on without the dashboard.", yes, false, a.leave())
	}
	return a.leave()
}

func (a *app) runLast() int {
	fmt.Println(a.last.title + " " + a.host + ": this session ends now.")
	cmd := a.last.command(false)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "dashboard:", err)
		return 1
	}
	return 0
}

func (a *app) leave() tea.Cmd {
	following := len(a.running()) > 0
	return func() tea.Msg {
		a.cancel()
		if following {
			time.Sleep(150 * time.Millisecond)
		}
		return tea.Quit()
	}
}

func (a *app) overlayKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, kBack), key.Matches(msg, kQuit), msg.String() == "?" && a.over == helpOverlay, msg.String() == "t" && a.over == tasksOverlay:
		a.over = noOverlay
		return nil
	}
	if a.over != tasksOverlay {
		return nil
	}
	switch {
	case msg.String() == "[" || key.Matches(msg, kPrev):
		a.taskSel = clamp(a.taskSel-1, 0, len(a.tasks)-1)
		a.taskOff = -1
	case msg.String() == "]" || key.Matches(msg, kNext):
		a.taskSel = clamp(a.taskSel+1, 0, len(a.tasks)-1)
		a.taskOff = -1
	case key.Matches(msg, kUp):
		a.taskOff = max(0, a.taskScroll()-1)
	case key.Matches(msg, kDown):
		a.taskOff = a.taskScroll() + 1
	case key.Matches(msg, kPgUp):
		a.taskOff = max(0, a.taskScroll()-10)
	case key.Matches(msg, kPgDown):
		a.taskOff = a.taskScroll() + 10
	case key.Matches(msg, kTop):
		a.taskOff = 0
	case key.Matches(msg, kBottom):
		a.taskOff = -1
	}
	return nil
}

type shellCmd struct {
	cmd    *exec.Cmd
	stdout io.Writer
	banner string
}

func (s *shellCmd) Run() error {
	fmt.Fprint(s.stdout, s.banner)
	return s.cmd.Run()
}

func (s *shellCmd) SetStdin(r io.Reader) {
	s.cmd.Stdin = r
}

func (s *shellCmd) SetStdout(w io.Writer) {
	s.cmd.Stdout = w
	s.stdout = w
}

func (s *shellCmd) SetStderr(w io.Writer) {
	s.cmd.Stderr = w
}

func (a *app) shell() tea.Cmd {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/bash"
	}
	return a.terminal(exec.Command(sh), "a shell on "+a.host, "")
}

func (a *app) terminal(cmd *exec.Cmd, where, into string) tea.Cmd {
	banner := "\n" + a.t.wordmark() + a.t.dim("  "+where+" — exit returns to the dashboard") + "\n\n"
	return tea.Exec(&shellCmd{cmd: cmd, banner: banner}, func(err error) tea.Msg { return shellDone{err, into} })
}

func (a *app) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "Citrine"
	if site := a.site(); site != "" {
		v.WindowTitle += " · " + site
	}
	if a.w == 0 || a.h == 0 {
		return v
	}
	if a.w < 50 || a.h < 14 {
		v.SetContent(lipgloss.Place(a.w, a.h, lipgloss.Center, lipgloss.Center,
			a.t.wordmark()+"\n\n"+a.t.dim("make the window a little bigger")))
		return v
	}
	roomy := a.h >= 34
	bodyH := a.h - 4
	parts := []string{a.header()}
	if roomy {
		bodyH -= 3
		parts = append(parts, "")
	}
	a.tabRow = len(parts)
	parts = append(parts, a.tabBar())
	if roomy {
		parts = append(parts, "")
	}
	parts = append(parts, a.body(bodyH))
	if roomy {
		parts = append(parts, "")
	}
	frame := strings.Join(append(parts, a.footer()), "\n")
	switch {
	case a.dlg != nil:
		frame = a.overlay(frame, a.dialogView())
	case a.over == helpOverlay:
		frame = a.overlay(frame, a.helpView())
	case a.over == tasksOverlay:
		frame = a.overlay(frame, a.tasksView())
	}
	v.SetContent(frame)
	return v
}

func (a *app) body(h int) string {
	s := &a.scrolls[a.cur]
	lines := strings.Split(s.view(a.active().view(a, a.w-4, h), h), "\n")
	bar := a.t.thumb(h, s.off, s.n)
	for i, l := range lines {
		lines[i] = "  " + fit(l, a.w-4) + " " + bar[i]
	}
	return strings.Join(lines, "\n")
}

func (a *app) header() string {
	t := a.t
	left := " " + t.wordmark() + "  " + t.soft(a.site())
	if uid := a.cfg.uid(); uid >= 0 && os.Geteuid() != uid {
		left += "  " + t.tint(lvWarn, "as "+currentUser())
	}
	var lock, version, uptime string
	if a.s.sudo == sudoLocked {
		lock = t.tint(lvWarn, "sudo locked · U")
	}
	if a.rel.version != "" {
		version = t.muted(a.rel.version)
	}
	if a.s.sys.uptime > 0 {
		uptime = t.dim("up ") + t.muted(span(a.s.sys.uptime))
	}
	now := t.text(time.Now().Format("15:04"))
	var right string
	for _, items := range [][]string{{lock, version, uptime, now}, {lock, version, uptime}, {lock, version}, {lock}} {
		var kept []string
		for _, it := range items {
			if it != "" {
				kept = append(kept, it)
			}
		}
		right = strings.Join(kept, t.edge(" · ")) + " "
		if width(left)+width(right) < a.w {
			break
		}
	}
	return spread(left, right, a.w)
}

func (a *app) site() string {
	if a.s.zone.ok {
		return a.s.zone.val
	}
	return a.host
}

func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return fmt.Sprint(os.Geteuid())
}

func (a *app) tabBar() string {
	t := a.t
	full := 2
	for _, tb := range a.tabs {
		full += width(tb.title()) + 4
	}
	compact := full > a.w
	heavy, light := "━", "─"
	if t.plain {
		heavy, light = "=", "-"
	}
	var top, under strings.Builder
	top.WriteString("  ")
	under.WriteString(t.edge(strings.Repeat(light, 2)))
	x := 2
	a.tabHits = a.tabHits[:0]
	for i, tb := range a.tabs {
		num := fmt.Sprint((i + 1) % 10)
		var cell string
		switch {
		case i == a.cur:
			cell = " " + t.accent(num) + " " + t.bold(tb.title()) + " "
		case compact:
			cell = " " + t.dim(num) + " "
		default:
			cell = " " + t.dim(num) + " " + t.faint(tb.title()) + " "
		}
		cw := width(cell)
		top.WriteString(cell)
		if i == a.cur {
			under.WriteString(t.accent(strings.Repeat(heavy, cw)))
		} else {
			under.WriteString(t.edge(strings.Repeat(light, cw)))
		}
		a.tabHits = append(a.tabHits, [2]int{x, x + cw})
		x += cw
	}
	under.WriteString(t.edge(strings.Repeat(light, max(0, a.w-x))))
	return top.String() + "\n" + under.String()
}

func (a *app) footer() string {
	t := a.t
	left := ""
	run := a.running()
	switch {
	case len(run) > 0:
		tk := run[len(run)-1]
		left = a.spin.View() + " " + t.text(tk.spec.title) + t.dim(" · "+elapsed(tk.took()))
		if len(run) > 1 {
			left += t.dim(fmt.Sprintf(" · +%d more", len(run)-1))
		}
		if l := tk.last(); l != "" {
			left += t.dim(" · ") + t.faint(l)
		}
	case !a.toast.at.IsZero() && time.Since(a.toast.at) < 8*time.Second:
		mark := t.dot(a.toast.lv)
		switch a.toast.lv {
		case lvOK:
			mark = t.tint(lvOK, "✓")
		case lvBad:
			mark = t.tint(lvBad, "✗")
		}
		left = mark + " " + t.soft(a.toast.text)
	}
	binds := a.active().keys(a)
	binds = append(binds, a.quitKey())
	left = clip(left, a.w/2+8)
	tail := a.help.ShortHelpView([]key.Binding{kHelp})
	room := a.w - 2 - width(left) - 2
	right := tail
	if keys := a.shortHelp(binds, room-width(tail)-width(a.help.ShortSeparator)); keys != "" {
		right = keys + a.help.Styles.ShortSeparator.Render(a.help.ShortSeparator) + tail
	}
	return " " + spread(left, right, a.w-2)
}

func (a *app) shortHelp(binds []key.Binding, w int) string {
	if w < 4 {
		return ""
	}
	for hw := w; ; hw-- {
		a.help.SetWidth(hw)
		if s := a.help.ShortHelpView(binds); width(s) <= w || hw <= 1 {
			if width(s) <= 1 {
				return ""
			}
			return s
		}
	}
}

func (a *app) overlay(base, top string) string {
	c := lipgloss.NewCanvas(a.w, a.h)
	c.Compose(lipgloss.NewLayer(base))
	for y := 0; y < a.h; y++ {
		for x := 0; x < a.w; x++ {
			if cell := c.CellAt(x, y); cell != nil {
				cell.Style.Fg = a.t.c.edge
				cell.Style.Attrs = 0
			}
		}
	}
	tw, th := lipgloss.Size(top)
	c.Compose(lipgloss.NewCompositor(lipgloss.NewLayer(top).X(max(0, (a.w-tw)/2)).Y(max(0, (a.h-th)/2))))
	return c.Render()
}

func (a *app) dialogView() string {
	t := a.t
	form := strings.TrimRight(a.dlg.form.View(), "\n")
	return t.box("", strings.Split(form, "\n"), a.dlg.w, 0)
}

func (a *app) huhStyles(danger bool) *huh.Styles {
	return huhStyles(a.t, danger)
}

func huhStyles(t theme, danger bool) *huh.Styles {
	s := huh.ThemeBase(t.dark)
	bar := t.c.accent
	if danger {
		bar = t.c.bad
	}
	s.Focused.Base = s.Focused.Base.BorderForeground(bar)
	s.Focused.Title = lipgloss.NewStyle().Foreground(t.c.text).Bold(true)
	s.Focused.Description = lipgloss.NewStyle().Foreground(t.c.faint)
	s.Focused.ErrorIndicator = lipgloss.NewStyle().Foreground(t.c.bad).SetString(" *")
	s.Focused.ErrorMessage = lipgloss.NewStyle().Foreground(t.c.bad)
	s.Focused.SelectSelector = lipgloss.NewStyle().Foreground(t.c.accent).SetString("› ")
	s.Focused.Option = lipgloss.NewStyle().Foreground(t.c.soft)
	s.Focused.SelectedOption = lipgloss.NewStyle().Foreground(t.c.accent)
	s.Focused.NextIndicator = lipgloss.NewStyle().Foreground(t.c.dim).MarginLeft(1).SetString("→")
	s.Focused.PrevIndicator = lipgloss.NewStyle().Foreground(t.c.dim).MarginRight(1).SetString("←")
	s.Focused.TextInput.Cursor = lipgloss.NewStyle().Foreground(t.c.accent)
	s.Focused.TextInput.Placeholder = lipgloss.NewStyle().Foreground(t.c.edge)
	s.Focused.TextInput.Prompt = lipgloss.NewStyle().Foreground(t.c.accent)
	s.Focused.TextInput.Text = lipgloss.NewStyle().Foreground(t.c.text)
	button := lipgloss.NewStyle().Padding(0, 2).MarginRight(1)
	s.Focused.FocusedButton = button.Foreground(t.c.ink).Background(bar).Bold(true)
	s.Focused.BlurredButton = button.Foreground(t.c.muted).Background(t.c.raised)
	s.Blurred = s.Focused
	s.Help.ShortKey = lipgloss.NewStyle().Foreground(t.c.muted)
	s.Help.ShortDesc = lipgloss.NewStyle().Foreground(t.c.dim)
	s.Help.ShortSeparator = lipgloss.NewStyle().Foreground(t.c.edge)
	return s
}

func (a *app) taskScroll() int {
	if a.taskOff >= 0 {
		return a.taskOff
	}
	tk := a.tasks[a.taskSel]
	return max(0, len(tk.lines)-a.taskRows())
}

func (a *app) taskRows() int {
	return clamp(a.h-10, 4, 24)
}

func (a *app) tasksView() string {
	t := a.t
	w := min(a.w-4, 120)
	tk := a.tasks[a.taskSel]
	state := t.tint(lvInfo, a.spin.View()+" running")
	switch {
	case tk.running:
	case tk.err != nil:
		state = t.tint(lvBad, "✗ "+short(tk.err))
	default:
		state = t.tint(lvOK, "✓ finished")
	}
	head := spread(t.bold(tk.spec.title)+"  "+state, t.dim(elapsed(tk.took())+fmt.Sprintf("  %d of %d", a.taskSel+1, len(a.tasks))), w-4)
	rows := a.taskRows()
	off := clamp(a.taskScroll(), 0, max(0, len(tk.lines)-rows))
	if a.taskOff >= 0 {
		a.taskOff = off
	}
	body := []string{head, t.rule(w - 4)}
	for i := off; i < len(tk.lines) && i < off+rows; i++ {
		body = append(body, a.logRow(tk.lines[i], w-4))
	}
	if len(tk.lines) == 0 {
		body = append(body, t.dim("waiting for output…"))
	}
	body = append(strings.Split(block(body, rows+2), "\n"), "", a.help.ShortHelpView([]key.Binding{
		bind("[ ]", "other tasks", "["), bind("↑↓", "scroll", "up"), bind("esc", "close", "esc"),
	}))
	return t.box(t.accent("Tasks"), body, w, 0)
}

func (a *app) logRow(l logLine, w int) string {
	t := a.t
	stamp := t.dim(l.at.Local().Format("15:04:05"))
	text := l.text
	switch {
	case l.prio <= 3:
		text = t.tint(lvBad, text)
	case l.prio == 4:
		text = t.tint(lvWarn, text)
	case l.source == "systemd" || l.prio >= 7:
		text = t.faint(text)
	default:
		text = t.soft(text)
	}
	return clip(stamp+" "+text, w)
}

func (a *app) helpView() string {
	t := a.t
	w := min(a.w-4, 86)
	kw := 14
	row := func(k, d string) string { return fit(t.accent(k), kw) + t.soft(d) }
	everywhere := []string{
		t.heading("Everywhere"),
		row("1–9, 0 · ←/→", "switch tab"),
		row("↑↓ pgup/pgdn", "move and scroll"),
		row("enter", "logs for the selection"),
		row("t", "tasks and their logs"),
		row("!", "a shell — exit comes back"),
		row("ctrl+r", "refresh now"),
		row("U", "unlock sudo"),
		row("q", a.quitKey().Help().Desc),
	}
	global := map[string]bool{kNext.Help().Key: true, kTasks.Help().Key: true, kShell.Help().Key: true}
	own := []string{t.heading(a.active().title())}
	for _, b := range a.active().keys(a) {
		if !global[b.Help().Key] {
			own = append(own, row(b.Help().Key, b.Help().Desc))
		}
	}
	if len(own) == 1 {
		own = append(own, t.dim("nothing of its own"))
	}
	lw := 0
	for _, l := range own {
		lw = max(lw, width(l))
	}
	if w-4-lw-2 < kw+30 {
		lines := append(append(own, ""), everywhere...)
		return t.box(t.accent("Keys"), strings.Split(block(lines, min(len(lines), a.h-4)), "\n"), w, 0)
	}
	n := max(len(everywhere), len(own))
	body := lipgloss.JoinHorizontal(lipgloss.Top, block(padAll(own, lw), n), "  ", block(everywhere, n))
	return t.box(t.accent("Keys"), strings.Split(body, "\n"), w, 0)
}

func errText(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errMissing):
		return "unavailable — " + err.Error()
	}
	return clean(err.Error())
}
