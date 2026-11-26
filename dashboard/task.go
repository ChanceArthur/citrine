package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
)

type spec struct {
	key   string
	title string
	argv  []string
	unit  string
	user  bool
	root  bool
	stdin string
	done  string
	after []kind
	last  bool
}

type logLine struct {
	at     time.Time
	prio   int
	source string
	text   string
}

type task struct {
	id      int
	spec    spec
	lines   []logLine
	started time.Time
	ended   time.Time
	err     error
	running bool
	ch      chan tea.Msg
}

type taskLine struct {
	id   int
	line logLine
}

type taskEnded struct {
	id  int
	err error
}

func transient(name, title string, user bool, argv ...string) ([]string, string) {
	unit := fmt.Sprintf("dashboard-%s-%d.service", name, time.Now().Unix())
	cmd := []string{"systemd-run"}
	if user {
		cmd = append(cmd, "--user")
	}
	cmd = append(cmd, "--wait", "--quiet", "--collect", "--unit="+unit, "--description="+title+" (dashboard)")
	return append(cmd, argv...), unit
}

func (s spec) command(detach bool) *exec.Cmd {
	argv := s.argv
	if s.root {
		argv = asRoot(argv...)
	}
	cmd := command(context.Background(), argv...)
	if detach {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	return cmd
}

func execute(ctx context.Context, id int, s spec, detach bool, ch chan<- tea.Msg) {
	var mu sync.Mutex
	emit := func(l logLine) {
		mu.Lock()
		defer mu.Unlock()
		select {
		case ch <- taskLine{id: id, line: l}:
		case <-ctx.Done():
		}
	}

	var followDone chan struct{}
	stopFollow := func() {}
	if s.unit != "" {
		args := []string{"journalctl", "-f", "-o", "json", "-n", "all", "--no-pager",
			"--since", "@" + strconv.FormatInt(time.Now().Unix()-1, 10), "-u", s.unit}
		if s.user {
			args = append(args[:1], append([]string{"--user"}, args[1:]...)...)
		}
		fctx, cancel := context.WithCancel(ctx)
		stopFollow = cancel
		follow := command(fctx, args...)
		if out, err := follow.StdoutPipe(); err == nil && follow.Start() == nil {
			followDone = make(chan struct{})
			go func() {
				defer close(followDone)
				sc := bufio.NewScanner(out)
				sc.Buffer(make([]byte, 64*1024), 1024*1024)
				for sc.Scan() {
					if e, ok := parseEntry(sc.Bytes()); ok && e.msg != "" {
						emit(logLine{at: e.at, prio: e.prio, source: e.source, text: e.msg})
					}
				}
				_ = follow.Wait()
			}()
		}
	}

	cmd := s.command(detach)
	if s.stdin != "" {
		cmd.Stdin = strings.NewReader(s.stdin)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	err = explain(cmd.Args, err, out.String(), nil)

	if followDone != nil {
		select {
		case <-time.After(900 * time.Millisecond):
		case <-ctx.Done():
		}
		stopFollow()
		<-followDone
	}
	prio := 6
	if err != nil {
		prio = 3
	}
	for _, l := range lines(out.Bytes()) {
		emit(logLine{at: time.Now(), prio: prio, text: clean(l)})
	}
	select {
	case ch <- taskEnded{id: id, err: err}:
	case <-ctx.Done():
	}
}

func listen(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

func (t *task) last() string {
	for i := len(t.lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(t.lines[i].text); s != "" {
			return s
		}
	}
	return ""
}

func (t *task) took() time.Duration {
	if t.running {
		return time.Since(t.started)
	}
	return t.ended.Sub(t.started)
}
