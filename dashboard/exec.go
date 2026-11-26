package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var (
	errMissing  = errors.New("not installed here")
	errPassword = errors.New("sudo wants a password — press U to unlock")
)

type cmdError struct {
	name   string
	code   int
	stderr string
}

func (e *cmdError) Error() string {
	msg := strings.TrimSpace(e.stderr)
	if i := strings.LastIndex(msg, "\n"); i >= 0 {
		msg = strings.TrimSpace(msg[i+1:])
	}
	if msg == "" {
		return fmt.Sprintf("%s exited %d", e.name, e.code)
	}
	return msg
}

func asRoot(args ...string) []string {
	if os.Geteuid() == 0 {
		return args
	}
	return append([]string{"sudo", "-n"}, args...)
}

func look(what ...string) []string {
	return asRoot(append([]string{libexec + "/look"}, what...)...)
}

func command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "LC_ALL=C.UTF-8")
	return cmd
}

func run(timeout time.Duration, args ...string) ([]byte, error) {
	return runIn(timeout, "", args...)
}

func runIn(timeout time.Duration, stdin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := command(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	err := cmd.Run()
	return stdout.Bytes(), explain(args, err, stderr.String(), ctx.Err())
}

func explain(args []string, err error, stderr string, ctxErr error) error {
	if err == nil {
		return nil
	}
	name := cmdName(args)
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return fmt.Errorf("%s took too long", name)
	}
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s: %w", args[0], errMissing)
	}
	if strings.Contains(stderr, "a password is required") || strings.Contains(stderr, "a terminal is required") {
		return errPassword
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if exit.ExitCode() == 127 && strings.Contains(stderr, "command not found") {
			return fmt.Errorf("%s: %w", name, errMissing)
		}
		return &cmdError{name: name, code: exit.ExitCode(), stderr: stderr}
	}
	return err
}

func cmdName(args []string) string {
	if args[0] == "sudo" && len(args) > 2 {
		args = args[2:]
	}
	if filepath.Base(args[0]) == "look" && len(args) > 1 {
		return args[1]
	}
	return filepath.Base(args[0])
}

func lines(b []byte) []string {
	var out []string
	for l := range strings.SplitSeq(strings.TrimRight(string(b), "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
