package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

type networkTab struct {
	sel, off int
}

func (networkTab) title() string { return "Network" }

func (networkTab) needs() []kind { return []kind{kTailnet, kServe, kIfaces, kContainers} }

func (networkTab) keys(*app) []key.Binding { return nil }

func (networkTab) selects(msg tea.KeyPressMsg) bool { return key.Matches(msg, kSelect) }

func (n *networkTab) key(a *app, msg tea.KeyPressMsg) tea.Cmd {
	count := len(a.s.tailnet.val.peers)
	switch {
	case key.Matches(msg, kUp):
		n.sel = clamp(n.sel-1, 0, count-1)
	case key.Matches(msg, kDown):
		n.sel = clamp(n.sel+1, 0, count-1)
	}
	return nil
}

func (n *networkTab) view(a *app, w, h int) string {
	tail := n.tailnet(a, w)
	ifs := n.interfaces(a, w)
	rest := h - strings.Count(tail, "\n") - strings.Count(ifs, "\n") - 5
	return tail + "\n\n" + ifs + "\n\n" + n.peers(a, w, max(3, rest))
}

func (networkTab) servedBy(a *app, target string) string {
	port := target
	if i := strings.LastIndex(target, ":"); i >= 0 {
		port = strings.Trim(target[i+1:], "/")
	}
	for _, q := range a.s.pods.val.quadlets {
		for _, p := range q.ports {
			parts := strings.Split(p, ":")
			if len(parts) >= 2 && parts[len(parts)-2] == port {
				return q.name
			}
		}
	}
	return ""
}

func (n networkTab) tailnet(a *app, w int) string {
	t := a.t
	r := a.s.tailnet
	title := t.bold("Tailnet")
	switch {
	case !r.ok && r.err != nil:
		return t.panel(title, t.tint(lvBad, errText(r.err)), w)
	case !r.ok:
		return t.panel(title, t.dim(a.spin.View()+" asking tailscaled"), w)
	}
	tn := r.val
	lv, state := lvOK, "online"
	switch {
	case tn.state != "Running":
		lv, state = lvBad, strings.ToLower(tn.state)
	case !tn.self.online:
		lv, state = lvWarn, "control plane unreachable"
	}
	lines := []string{t.dot(lv) + " " + t.tint(lv, state) + "  " + t.text(tn.self.dns)}
	var facts []string
	facts = append(facts, tn.self.ips...)
	facts = append(facts, tn.self.tags...)
	if tn.self.exitOption {
		facts = append(facts, "exit node offered")
	}
	if tn.version != "" {
		facts = append(facts, "tailscale "+tn.version)
	}
	lines = append(lines, "  "+t.muted(strings.Join(facts, t.dim(" · "))))
	for _, h := range tn.health {
		lines = append(lines, "  "+t.tint(lvWarn, "! "+h))
	}
	if routes := a.s.serve.val; len(routes) > 0 {
		var parts []string
		for _, rt := range routes {
			who := n.servedBy(a, rt.target)
			if who == "" {
				who = rt.target
			}
			parts = append(parts, t.accent(":"+rt.port)+t.dim(" → ")+t.muted(who))
		}
		lines = append(lines, "", t.faint("serves  ")+strings.Join(parts, t.dim("  ·  ")))
	} else if err := a.s.serve.err; err != nil {
		lines = append(lines, "", t.faint("serves  ")+t.dim(errText(err)))
	}
	return t.panel(title, strings.Join(lines, "\n"), w)
}

func (networkTab) interfaces(a *app, w int) string {
	t := a.t
	r := a.s.ifaces
	if !r.ok {
		if r.err != nil {
			return t.muted(errText(r.err))
		}
		return t.dim(a.spin.View() + " reading interfaces")
	}
	spark := 12
	cols := []col{{title: "interface", w: 12}, {title: "state", w: 8}, {title: "addresses", w: 34}, {title: "speed", w: 8, right: true}, {title: "traffic", w: 24}, {title: "", w: 0}}
	switch {
	case w < 72:
		cols = []col{{title: "interface", w: 12}, {title: "state", w: 8}, {title: "addresses", w: 0}}
	case w < 110:
		cols = []col{{title: "interface", w: 12}, {title: "state", w: 8}, {title: "addresses", w: 0}, {title: "traffic", w: 24}}
	}
	var rows [][]string
	for _, it := range r.val {
		lv := lvNone
		switch it.state {
		case "up":
			lv = lvOK
		case "down":
			lv = lvBad
		}
		speed := t.dim("—")
		if it.speed > 0 {
			speed = t.muted(linkSpeed(it.speed))
		}
		traffic := t.dim("↓ ") + t.muted(rate(it.rxRate)) + t.dim("  ↑ ") + t.muted(rate(it.txRate))
		cells := []string{t.soft(it.name), t.dot(lv) + " " + t.tint(lv, orDash(it.state)), t.muted(strings.Join(it.addrs, ", "))}
		switch len(cols) {
		case 6:
			cells = append(cells, speed, traffic, t.spark(a.s.ifHist[it.name], spark, 0, t.c.info))
		case 4:
			cells = append(cells, traffic)
		}
		rows = append(rows, cells)
	}
	return strings.Join(t.table(cols, rows, -1, w, len(rows)+1, nil), "\n")
}

func linkSpeed(mbps int) string {
	if mbps >= 1000 && mbps%1000 == 0 {
		return fmt.Sprintf("%d Gb/s", mbps/1000)
	}
	if mbps >= 1000 {
		return fmt.Sprintf("%.1f Gb/s", float64(mbps)/1000)
	}
	return fmt.Sprintf("%d Mb/s", mbps)
}

func (n *networkTab) peers(a *app, w, h int) string {
	t := a.t
	peers := a.s.tailnet.val.peers
	if len(peers) == 0 {
		return ""
	}
	nw := 0
	for _, p := range peers {
		nw = max(nw, width(p.name)+2)
	}
	cols := []col{{title: "peer", w: clamp(nw, 8, 28)}, {title: "os", w: 9}, {title: "address", w: 16}, {title: "status", w: 0}}
	if w < 64 {
		cols = []col{{title: "peer", w: clamp(nw, 8, 28)}, {title: "address", w: 16}, {title: "status", w: 0}}
	}
	var rows [][]string
	online := 0
	for i, p := range peers {
		lv, status := lvNone, "last seen "+ago(p.lastSeen)
		if p.online {
			online++
			lv, status = lvOK, "online"
			if p.active {
				route := "relay " + p.relay
				if p.direct {
					route = "direct"
				}
				status = "active · " + route
			}
		}
		if p.exitNode {
			status += " · exit node"
		}
		ip := ""
		if len(p.ips) > 0 {
			ip = p.ips[0]
		}
		name := t.soft(p.name)
		if i == n.sel {
			name = t.bold(p.name)
		}
		row := []string{t.dot(lv) + " " + name, t.faint(p.os), t.muted(ip), t.tint(lv, status)}
		if len(cols) == 3 {
			row = []string{row[0], row[2], row[3]}
		}
		rows = append(rows, row)
	}
	n.sel = clamp(n.sel, 0, len(rows)-1)
	head := t.heading("Peers") + t.dim(fmt.Sprintf("  %d of %d online", online, len(peers)))
	return strings.Join(append([]string{head}, t.table(cols, rows, n.sel, w, h-1, &n.off)...), "\n")
}
