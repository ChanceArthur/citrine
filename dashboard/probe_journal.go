package main

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

type entry struct {
	at     time.Time
	prio   int
	source string
	msg    string
	cursor string
}

func parseEntry(line []byte) (entry, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return entry{}, false
	}
	field := func(k string) string {
		v, ok := raw[k]
		if !ok {
			return ""
		}
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
		var bs []byte
		if json.Unmarshal(v, &bs) == nil {
			return string(bs)
		}
		var ns []int
		if json.Unmarshal(v, &ns) == nil {
			b := make([]byte, len(ns))
			for i, n := range ns {
				b[i] = byte(n)
			}
			return string(b)
		}
		return ""
	}
	e := entry{msg: clean(field("MESSAGE")), cursor: field("__CURSOR"), prio: 6}
	if us, err := strconv.ParseInt(field("__REALTIME_TIMESTAMP"), 10, 64); err == nil {
		e.at = time.UnixMicro(us)
	}
	if p, err := strconv.Atoi(field("PRIORITY")); err == nil {
		e.prio = p
	}
	if p, ok := statedLevel(e.msg); ok && field("CONTAINER_NAME") != "" {
		e.prio = p
	}
	for _, k := range []string{"CONTAINER_NAME", "SYSLOG_IDENTIFIER", "_COMM"} {
		if v := field(k); v != "" {
			e.source = clean(v)
			break
		}
	}
	return e, true
}

func levelOf(word string) (int, bool) {
	switch strings.ToUpper(word) {
	case "TRACE", "DEBUG", "DBG", "VERBOSE":
		return 7, true
	case "INFO", "INF", "LOG":
		return 6, true
	case "NOTICE":
		return 5, true
	case "WARN", "WARNING", "WRN":
		return 4, true
	case "ERROR", "ERR":
		return 3, true
	case "CRITICAL", "CRIT", "FATAL", "FTL", "PANIC":
		return 2, true
	}
	return 0, false
}

func statedLevel(msg string) (int, bool) {
	if strings.HasPrefix(msg, "{") {
		var j struct {
			Level string `json:"level"`
		}
		if json.Unmarshal([]byte(msg), &j) == nil {
			return levelOf(j.Level)
		}
	}
	for i, f := range strings.Fields(msg[:min(len(msg), 120)]) {
		if i == 8 {
			break
		}
		word, colon := strings.CutSuffix(strings.Trim(f, "[]"), ":")
		if !colon && word != strings.ToUpper(word) {
			continue
		}
		if p, ok := levelOf(word); ok {
			return p, true
		}
	}
	return 0, false
}

type query struct {
	user  bool
	unit  string
	prio  int
	lines int
	after string
	since time.Time
}

func (q query) args(follow bool) []string {
	args := []string{"journalctl", "-o", "json", "--output-fields=MESSAGE,PRIORITY,CONTAINER_NAME,SYSLOG_IDENTIFIER,_COMM", "--no-pager"}
	if q.user {
		args = append(args, "--user")
	}
	if q.unit != "" {
		args = append(args, "-u", q.unit)
	}
	if q.prio > 0 && q.prio < 7 {
		args = append(args, "-p", strconv.Itoa(q.prio))
	}
	if !q.since.IsZero() {
		args = append(args, "--since", "@"+strconv.FormatInt(q.since.Unix(), 10))
	}
	if q.after != "" {
		args = append(args, "--after-cursor", q.after)
	} else if q.lines > 0 {
		args = append(args, "-n", strconv.Itoa(q.lines))
	}
	if follow {
		args = append(args, "-f")
	}
	return args
}

func readJournal(q query) ([]entry, error) {
	b, err := run(20*time.Second, q.args(false)...)
	if err != nil && len(b) == 0 {
		return nil, err
	}
	var out []entry
	for line := range bytes.SplitSeq(b, []byte("\n")) {
		if e, ok := parseEntry(line); ok {
			out = append(out, e)
		}
	}
	return out, nil
}
