package main

import (
	"encoding/json"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

type peer struct {
	name       string
	dns        string
	os         string
	ips        []string
	tags       []string
	online     bool
	active     bool
	exitNode   bool
	exitOption bool
	relay      string
	direct     bool
	lastSeen   time.Time
	rx, tx     int64
}

type tailnet struct {
	state   string
	version string
	self    peer
	peers   []peer
	health  []string
	suffix  string
}

type rawPeer struct {
	HostName       string
	DNSName        string
	OS             string
	TailscaleIPs   []string
	Tags           []string
	Online         bool
	Active         bool
	ExitNode       bool
	ExitNodeOption bool
	Relay          string
	CurAddr        string
	LastSeen       time.Time
	RxBytes        int64
	TxBytes        int64
}

func (r rawPeer) peer() peer {
	p := peer{
		name: clean(r.HostName), dns: clean(strings.TrimSuffix(r.DNSName, ".")), os: clean(r.OS), ips: cleanAll(r.TailscaleIPs), tags: cleanAll(r.Tags),
		online: r.Online, active: r.Active, exitNode: r.ExitNode, exitOption: r.ExitNodeOption,
		relay: clean(r.Relay), direct: r.CurAddr != "", lastSeen: r.LastSeen, rx: r.RxBytes, tx: r.TxBytes,
	}
	if label, _, ok := strings.Cut(p.dns, "."); ok && label != "" {
		p.name = label
	}
	return p
}

func tailscaleStatus() (tailnet, error) {
	b, err := run(10*time.Second, "tailscale", "status", "--json")
	if err != nil {
		return tailnet{}, err
	}
	return parseTailnet(b)
}

func parseTailnet(b []byte) (tailnet, error) {
	var raw struct {
		Version        string
		BackendState   string
		Self           *rawPeer
		Peer           map[string]rawPeer
		Health         []string
		MagicDNSSuffix string
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return tailnet{}, err
	}
	tn := tailnet{state: clean(raw.BackendState), version: clean(raw.Version), suffix: clean(raw.MagicDNSSuffix)}
	for _, h := range raw.Health {
		if !strings.Contains(h, "SELinux") {
			tn.health = append(tn.health, clean(h))
		}
	}
	if raw.Self != nil {
		tn.self = raw.Self.peer()
	}
	for _, p := range raw.Peer {
		tn.peers = append(tn.peers, p.peer())
	}
	sort.Slice(tn.peers, func(i, j int) bool {
		a, b := tn.peers[i], tn.peers[j]
		if a.online != b.online {
			return a.online
		}
		return strings.ToLower(a.name) < strings.ToLower(b.name)
	})
	if i := strings.Index(tn.version, "-"); i > 0 {
		tn.version = tn.version[:i]
	}
	return tn, nil
}

func (t tailnet) ip4() string {
	for _, ip := range t.self.ips {
		if a := net.ParseIP(ip); a != nil && a.To4() != nil {
			return ip
		}
	}
	if len(t.self.ips) > 0 {
		return t.self.ips[0]
	}
	return ""
}

type route struct {
	port   string
	target string
}

func tailscaleServe() ([]route, error) {
	b, err := run(10*time.Second, look("serve")...)
	if err != nil {
		return nil, err
	}
	return parseServe(b)
}

func parseServe(b []byte) ([]route, error) {
	var raw struct {
		TCP map[string]struct {
			HTTPS      bool
			HTTP       bool
			TCPForward string
		}
		Web map[string]struct {
			Handlers map[string]struct {
				Proxy string
				Path  string
				Text  string
			}
		}
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	var out []route
	for hp, web := range raw.Web {
		_, port, err := net.SplitHostPort(hp)
		if err != nil {
			port = hp
		}
		for mount, h := range web.Handlers {
			target := h.Proxy
			if target == "" {
				target = h.Path
			}
			if mount != "/" {
				port += mount
			}
			out = append(out, route{port: clean(port), target: clean(target)})
		}
	}
	for port, tcp := range raw.TCP {
		if tcp.TCPForward != "" {
			out = append(out, route{port: clean(port), target: "tcp " + clean(tcp.TCPForward)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := strconv.Atoi(strings.SplitN(out[i].port, "/", 2)[0])
		b, _ := strconv.Atoi(strings.SplitN(out[j].port, "/", 2)[0])
		return a < b
	})
	return out, nil
}
