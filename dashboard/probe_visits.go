package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"
	"sort"
	"strconv"
	"time"
)

type visitor struct {
	ip       string
	country  string
	agent    string
	referer  string
	first    time.Time
	last     time.Time
	requests int
	status   int
	paths    []string
	cf       map[string]string
}

type visits struct {
	requests int
	visitors []visitor
}

func readVisits(window time.Duration) (visits, error) {
	since := time.Now().Add(-window)
	cmd := command(context.Background(), "journalctl", "--user", "-u", "citrine-visits.service", "--no-pager", "-o", "cat",
		"--since", "@"+strconv.FormatInt(since.Unix(), 10))
	out, err := cmd.StdoutPipe()
	if err != nil {
		return visits{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return visits{}, explain(cmd.Args, err, "", nil)
	}
	v := parseVisits(out)
	if err := cmd.Wait(); err != nil && v.requests == 0 {
		return v, explain(cmd.Args, err, stderr.String(), nil)
	}
	return v, nil
}

func parseVisits(r io.Reader) visits {
	var v visits
	by := map[string]*visitor{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var e struct {
			Msg       string            `json:"msg"`
			Time      time.Time         `json:"time"`
			Status    int               `json:"status"`
			Path      string            `json:"path"`
			UserAgent string            `json:"userAgent"`
			Referer   string            `json:"referer"`
			CF        map[string]string `json:"cf"`
		}
		if json.Unmarshal(line, &e) != nil || e.Msg != "visit" {
			continue
		}
		e.Path, e.UserAgent, e.Referer = clean(e.Path), clean(e.UserAgent), clean(e.Referer)
		for name, value := range e.CF {
			e.CF[name] = clean(value)
		}
		v.requests++
		ip := e.CF["cf-connecting-ip"]
		vis := by[ip]
		if vis == nil {
			vis = &visitor{ip: ip, first: e.Time}
			by[ip] = vis
		}
		vis.requests++
		vis.last, vis.status, vis.cf = e.Time, e.Status, e.CF
		vis.country, vis.agent, vis.referer = e.CF["cf-ipcountry"], e.UserAgent, e.Referer
		vis.paths = append(slices.DeleteFunc(vis.paths, func(p string) bool { return p == e.Path }), e.Path)
		if len(vis.paths) > 8 {
			vis.paths = vis.paths[1:]
		}
	}
	for _, vis := range by {
		v.visitors = append(v.visitors, *vis)
	}
	sort.Slice(v.visitors, func(i, j int) bool { return v.visitors[i].last.After(v.visitors[j].last) })
	if len(v.visitors) > 200 {
		v.visitors = v.visitors[:200]
	}
	return v
}
