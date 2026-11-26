package main

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
)

type unit struct {
	id          string
	description string
	load        string
	active      string
	sub         string
	result      string
	fileState   string
	since       time.Time
	exited      time.Time
	exitStatus  int
	next        time.Time
	last        time.Time
	triggers    string
	restarts    int
	user        bool
}

var unitProps = "Id,Description,LoadState,ActiveState,SubState,Result,UnitFileState," +
	"StateChangeTimestamp,ExecMainExitTimestamp,ExecMainStatus," +
	"NextElapseUSecRealtime,LastTriggerUSec,Triggers,NRestarts"

func systemctl(user bool, args ...string) []string {
	cmd := []string{"systemctl"}
	if user {
		cmd = append(cmd, "--user")
	}
	return append(cmd, args...)
}

func showUnits(user bool, names []string) (map[string]unit, error) {
	if len(names) == 0 {
		return map[string]unit{}, nil
	}
	args := append([]string{"show", "--timestamp=unix", "-p", unitProps, "--"}, names...)
	b, err := run(10*time.Second, systemctl(user, args...)...)
	if err != nil {
		return nil, err
	}
	units := parseShow(string(b), user)
	for name, u := range units {
		if strings.HasSuffix(name, ".service") && u.load == "loaded" && u.active == "inactive" && u.exited.IsZero() {
			o := pastRun(user, name)
			u.exited, u.result = o.at, o.result
			units[name] = u
		}
	}
	return units, nil
}

const (
	msgUnitSucceeded = "7ad2d189f7e94e70a38c781354912448"
	msgUnitFinished  = "39f53479d3a045ac8e11786248231fbf"
	msgUnitFailed    = "d9b373ed55a64feb8242e02dbe79a49c"
)

type outcome struct {
	at     time.Time
	result string
}

var (
	pastMu   sync.Mutex
	pastRuns = map[string]outcome{}
)

func pastRun(user bool, name string) outcome {
	key := strconv.FormatBool(user) + name
	pastMu.Lock()
	o, ok := pastRuns[key]
	pastMu.Unlock()
	if ok {
		return o
	}
	args := []string{"journalctl", "-o", "json", "--output-fields=MESSAGE_ID,UNIT_RESULT", "-n", "1", "--no-pager"}
	if user {
		args = append(args, "--user", "USER_UNIT="+name)
	} else {
		args = append(args, "_PID=1", "UNIT="+name)
	}
	args = append(args, "MESSAGE_ID="+msgUnitSucceeded, "MESSAGE_ID="+msgUnitFinished, "MESSAGE_ID="+msgUnitFailed)
	b, _ := run(10*time.Second, args...)
	o = parseOutcome(b)
	pastMu.Lock()
	pastRuns[key] = o
	pastMu.Unlock()
	return o
}

func parseOutcome(b []byte) outcome {
	var e struct {
		At     string `json:"__REALTIME_TIMESTAMP"`
		ID     string `json:"MESSAGE_ID"`
		Result string `json:"UNIT_RESULT"`
	}
	if json.Unmarshal(bytes.TrimSpace(b), &e) != nil {
		return outcome{}
	}
	us, err := strconv.ParseInt(e.At, 10, 64)
	if err != nil {
		return outcome{}
	}
	o := outcome{at: time.UnixMicro(us), result: "success"}
	if e.ID == msgUnitFailed {
		o.result = e.Result
	}
	return o
}

func parseShow(s string, user bool) map[string]unit {
	out := map[string]unit{}
	for blk := range strings.SplitSeq(s, "\n\n") {
		u := unit{user: user}
		for line := range strings.SplitSeq(blk, "\n") {
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			switch k {
			case "Id":
				u.id = v
			case "Description":
				u.description = clean(v)
			case "LoadState":
				u.load = v
			case "ActiveState":
				u.active = v
			case "SubState":
				u.sub = v
			case "Result":
				u.result = v
			case "UnitFileState":
				u.fileState = v
			case "StateChangeTimestamp":
				u.since = unixStamp(v)
			case "ExecMainExitTimestamp":
				u.exited = unixStamp(v)
			case "ExecMainStatus":
				u.exitStatus, _ = strconv.Atoi(v)
			case "NextElapseUSecRealtime":
				u.next = unixStamp(v)
			case "LastTriggerUSec":
				u.last = unixStamp(v)
			case "Triggers":
				u.triggers, _, _ = strings.Cut(v, " ")
			case "NRestarts":
				u.restarts, _ = strconv.Atoi(v)
			}
		}
		if u.id != "" {
			out[u.id] = u
		}
	}
	return out
}

func unixStamp(v string) time.Time {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "@") {
		return time.Time{}
	}
	f, err := strconv.ParseFloat(v[1:], 64)
	if err != nil || f <= 0 {
		return time.Time{}
	}
	sec := int64(f)
	return time.Unix(sec, int64((f-float64(sec))*1e9))
}

func listUnitFiles(user bool, patterns ...string) ([]string, error) {
	args := append([]string{"list-unit-files", "--no-legend", "--plain", "--no-pager"}, patterns...)
	b, err := run(10*time.Second, systemctl(user, args...)...)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range lines(b) {
		name := strings.Fields(l)[0]
		if strings.Contains(name, "@.") {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

type failedUnit struct {
	name        string
	description string
	user        bool
}

func listFailed(user bool) ([]failedUnit, error) {
	b, err := run(10*time.Second, systemctl(user, "list-units", "--failed", "--all", "--plain", "--no-legend", "--no-pager")...)
	if err != nil {
		return nil, err
	}
	return parseFailed(b, user), nil
}

func parseFailed(b []byte, user bool) []failedUnit {
	var out []failedUnit
	for _, l := range lines(b) {
		f := strings.Fields(strings.TrimLeft(l, "● *"))
		if len(f) < 4 {
			continue
		}
		out = append(out, failedUnit{name: f[0], description: clean(strings.Join(f[4:], " ")), user: user})
	}
	return out
}

func (u unit) state() (level, string) {
	switch u.active {
	case "active":
		if u.sub == "exited" {
			return lvOK, "done"
		}
		return lvOK, u.sub
	case "activating":
		return lvInfo, "running"
	case "deactivating", "reloading":
		return lvWarn, u.active
	case "failed":
		return lvBad, "failed"
	case "inactive":
		if u.result != "" && u.result != "success" {
			return lvBad, "last run " + strings.ReplaceAll(u.result, "-", " ")
		}
		if u.load == "not-found" {
			return lvNone, "not installed"
		}
		return lvNone, "idle"
	}
	if u.load == "not-found" {
		return lvNone, "not installed"
	}
	return lvNone, u.active
}

func (u unit) lastRun() (level, string) {
	if u.exited.IsZero() {
		return lvNone, "never ran"
	}
	if u.result == "success" && u.exitStatus == 0 {
		return lvOK, "ok " + ago(u.exited)
	}
	return lvBad, "failed " + ago(u.exited)
}

func baseName(unitName string) string {
	for _, suf := range []string{".service", ".timer", ".socket", ".target", ".mount"} {
		unitName = strings.TrimSuffix(unitName, suf)
	}
	return unitName
}
