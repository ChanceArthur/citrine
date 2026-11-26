package main

import (
	"errors"
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type kind int

const (
	kSys kind = iota
	kContainers
	kStats
	kImages
	kPending
	kUnits
	kUserUnits
	kFailed
	kOstree
	kVersions
	kTailnet
	kServe
	kPool
	kScrub
	kDisks
	kSmart
	kSnapshots
	kDrive
	kTraffic
	kStops
	kVisits
	kIfaces
	kAlerts
	kSudo
	kZone
	kinds
)

var every = [kinds]time.Duration{
	kSys:        2 * time.Second,
	kContainers: 5 * time.Second,
	kStats:      10 * time.Second,
	kImages:     time.Minute,
	kPending:    30 * time.Minute,
	kUnits:      10 * time.Second,
	kUserUnits:  5 * time.Second,
	kFailed:     30 * time.Second,
	kOstree:     30 * time.Second,
	kTailnet:    15 * time.Second,
	kServe:      2 * time.Minute,
	kPool:       15 * time.Second,
	kScrub:      30 * time.Second,
	kDisks:      time.Minute,
	kSmart:      15 * time.Minute,
	kSnapshots:  15 * time.Minute,
	kDrive:      30 * time.Second,
	kTraffic:    2 * time.Minute,
	kStops:      2 * time.Minute,
	kVisits:     time.Minute,
	kIfaces:     3 * time.Second,
	kAlerts:     time.Minute,
	kZone:       time.Minute,
}

type result[T any] struct {
	val T
	err error
	ok  bool
	at  time.Time
}

func (r *result[T]) set(v T, err error) {
	r.err = err
	r.at = time.Now()
	if err == nil {
		r.val, r.ok = v, true
	}
}

type sched struct {
	at   time.Time
	busy bool
}

type loaded struct {
	k      kind
	locked bool
	apply  func(a *app) tea.Cmd
}

func fetch[T any](k kind, f func() (T, error), apply func(a *app, v T, err error) tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		v, err := f()
		return loaded{k: k, locked: errors.Is(err, errPassword), apply: func(a *app) tea.Cmd { return apply(a, v, err) }}
	}
}

func keep[T any](pick func(s *store) *result[T]) func(a *app, v T, err error) tea.Cmd {
	return func(a *app, v T, err error) tea.Cmd {
		pick(&a.s).set(v, err)
		return nil
	}
}

type podState struct {
	quadlets []quadlet
	ps       map[string]psEntry
}

type unitSet struct {
	units map[string]unit
	names []string
}

type sudoState int

const (
	sudoUnknown sudoState = iota
	sudoFree
	sudoAsks
	sudoLocked
	sudoAbsent
)

type store struct {
	prev      sysReading
	sys       sysReading
	cpu       float64
	mem       float64
	rx, tx    float64
	cpuHist   []float64
	netHist   []float64
	pods      result[podState]
	stats     result[map[string]ctrStats]
	images    result[map[string]imageInfo]
	pending   result[map[string]string]
	units     result[unitSet]
	userUnits result[unitSet]
	failed    result[[]failedUnit]
	ostree    result[ostreeStatus]
	versions  result[[]string]
	tailnet   result[tailnet]
	serve     result[[]route]
	pool      result[pool]
	scrub     result[scrub]
	disks     result[[]disk]
	smart     result[map[string]smart]
	snapshots result[[]snapshot]
	drive     result[drive]
	traffic   result[traffic]
	stops     result[[]shieldEvent]
	visits    result[visits]
	ifaces    result[[]iface]
	alerts    result[[]entry]
	zone      result[string]
	ifPrev    map[string]ifCounters
	ifAt      time.Time
	ifHist    map[string][]float64
	sudo      sudoState
}

var systemExtras = []string{
	"rpm-ostreed-automatic.timer", "rpm-ostreed-automatic.service",
	"tailscaled.service", "smb.service", "sshd.service",
}

var userExtras = []string{"podman-auto-update.timer", "podman-auto-update.service", "citrine-data.service"}

func (a *app) load(k kind) tea.Cmd {
	cfg := a.cfg
	switch k {
	case kSys:
		return fetch(k, readSystem, func(a *app, v sysReading, err error) tea.Cmd {
			if err != nil {
				return nil
			}
			return a.applySys(v)
		})
	case kContainers:
		dir := cfg.quadlets()
		return fetch(k, func() (podState, error) {
			qs, err := readQuadlets(dir)
			if err != nil {
				return podState{}, err
			}
			ps, err := podmanPS()
			return podState{quadlets: qs, ps: ps}, err
		}, keep(func(s *store) *result[podState] { return &s.pods }))
	case kStats:
		return fetch(k, podmanStats, keep(func(s *store) *result[map[string]ctrStats] { return &s.stats }))
	case kImages:
		return fetch(k, podmanImages, keep(func(s *store) *result[map[string]imageInfo] { return &s.images }))
	case kPending:
		dir := cfg.quadlets()
		return fetch(k, func() (map[string]string, error) { return readPending(dir) }, keep(func(s *store) *result[map[string]string] { return &s.pending }))
	case kUnits:
		return fetch(k, func() (unitSet, error) {
			names, err := listUnitFiles(false, "citrine-*")
			if err != nil {
				return unitSet{}, err
			}
			names = append(names, systemExtras...)
			units, err := showUnits(false, names)
			return unitSet{units: units, names: names}, err
		}, keep(func(s *store) *result[unitSet] { return &s.units }))
	case kUserUnits:
		names := append([]string{}, userExtras...)
		for _, q := range a.s.pods.val.quadlets {
			names = append(names, q.unit)
		}
		return fetch(k, func() (unitSet, error) {
			units, err := showUnits(true, names)
			return unitSet{units: units, names: names}, err
		}, keep(func(s *store) *result[unitSet] { return &s.userUnits }))
	case kFailed:
		return fetch(k, func() ([]failedUnit, error) {
			sys, err := listFailed(false)
			if err != nil {
				return nil, err
			}
			usr, _ := listFailed(true)
			return append(sys, usr...), nil
		}, keep(func(s *store) *result[[]failedUnit] { return &s.failed }))
	case kOstree:
		return fetch(k, rpmOstreeStatus, keep(func(s *store) *result[ostreeStatus] { return &s.ostree }))
	case kVersions:
		repo := cfg.repo()
		return fetch(k, func() ([]string, error) { return listVersions(repo) }, keep(func(s *store) *result[[]string] { return &s.versions }))
	case kTailnet:
		return fetch(k, tailscaleStatus, keep(func(s *store) *result[tailnet] { return &s.tailnet }))
	case kServe:
		return fetch(k, tailscaleServe, keep(func(s *store) *result[[]route] { return &s.serve }))
	case kPool:
		label, point := cfg["POOL_LABEL"], cfg["POOL_MOUNT"]
		return fetch(k, func() (pool, error) { return readPool(label, point) }, func(a *app, v pool, err error) tea.Cmd {
			a.s.pool.set(v, err)
			return a.animate("pool", v.frac())
		})
	case kScrub:
		return fetch(k, readScrub, func(a *app, v scrub, err error) tea.Cmd {
			a.s.scrub.set(v, err)
			return a.animate("scrub", v.progress)
		})
	case kDisks:
		pl, bl := cfg["POOL_LABEL"], cfg["BACKUP_LABEL"]
		return fetch(k, func() ([]disk, error) { return readDisks(pl, bl) }, keep(func(s *store) *result[[]disk] { return &s.disks }))
	case kSmart:
		pl, bl := cfg["POOL_LABEL"], cfg["BACKUP_LABEL"]
		return fetch(k, func() (map[string]smart, error) {
			disks, err := readDisks(pl, bl)
			if err != nil {
				return nil, err
			}
			return readSmart(disks), nil
		}, keep(func(s *store) *result[map[string]smart] { return &s.smart }))
	case kAlerts:
		return fetch(k, func() ([]entry, error) {
			es, err := readJournal(query{prio: 3, lines: 2000, since: time.Now().Add(-24 * time.Hour)})
			es = slices.DeleteFunc(es, func(e entry) bool { return e.prio > 3 })
			return es[max(0, len(es)-6):], err
		}, keep(func(s *store) *result[[]entry] { return &s.alerts }))
	case kSnapshots:
		return fetch(k, r2Snapshots, keep(func(s *store) *result[[]snapshot] { return &s.snapshots }))
	case kDrive:
		point := cfg["BACKUP_MOUNT"]
		return fetch(k, func() (drive, error) { return readDrive(point) }, keep(func(s *store) *result[drive] { return &s.drive }))
	case kTraffic:
		window, token, zone := a.visitWindow(), cfg["CF_API_TOKEN"], cfg["CF_ZONE_ID"]
		return fetch(k, func() (traffic, error) { return readTraffic(token, zone, window, 48) }, keep(func(s *store) *result[traffic] { return &s.traffic }))
	case kStops:
		window, token, zone := a.visitWindow(), cfg["CF_API_TOKEN"], cfg["CF_ZONE_ID"]
		return fetch(k, func() ([]shieldEvent, error) { return readStops(token, zone, window) }, keep(func(s *store) *result[[]shieldEvent] { return &s.stops }))
	case kVisits:
		window := a.visitWindow()
		return fetch(k, func() (visits, error) { return readVisits(window) }, keep(func(s *store) *result[visits] { return &s.visits }))
	case kIfaces:
		return fetch(k, readIfaces, func(a *app, v []iface, err error) tea.Cmd {
			a.applyIfaces(v, err)
			return nil
		})
	case kZone:
		token, zone := cfg["CF_API_TOKEN"], cfg["CF_ZONE_ID"]
		if a.s.zone.ok || token == "" || zone == "" {
			return nil
		}
		return fetch(k, func() (string, error) { return readZone(token, zone) }, keep(func(s *store) *result[string] { return &s.zone }))
	case kSudo:
		return fetch(k, probeSudo, func(a *app, v sudoState, _ error) tea.Cmd {
			a.s.sudo = v
			return nil
		})
	}
	return nil
}

func probeSudo() (sudoState, error) {
	if os.Geteuid() == 0 {
		return sudoFree, nil
	}
	out, err := run(5*time.Second, "sudo", "-n", "-l")
	switch {
	case err == nil && strings.Contains(string(out), "NOPASSWD: ALL"):
		return sudoFree, nil
	case err == nil || errors.Is(err, errPassword):
		return sudoAsks, nil
	}
	return sudoAbsent, nil
}

func push(hist []float64, v float64, n int) []float64 {
	hist = append(hist, v)
	if len(hist) > n {
		hist = hist[len(hist)-n:]
	}
	return hist
}

func (a *app) applySys(v sysReading) tea.Cmd {
	s := &a.s
	s.prev, s.sys = s.sys, v
	if !s.prev.at.IsZero() {
		s.cpu = cpuBusy(s.prev.cpu, v.cpu)
		s.cpuHist = push(s.cpuHist, s.cpu, 120)
		dt := v.at.Sub(s.prev.at).Seconds()
		if dt > 0 {
			var rx, tx uint64
			for name, c := range v.net {
				if p, ok := s.prev.net[name]; ok && physical(name) {
					rx += c.rx - min(c.rx, p.rx)
					tx += c.tx - min(c.tx, p.tx)
				}
			}
			s.rx, s.tx = float64(rx)/dt, float64(tx)/dt
			s.netHist = push(s.netHist, s.rx+s.tx, 120)
		}
	}
	if v.memTotal > 0 {
		s.mem = float64(v.memTotal-min(v.memTotal, v.memAvail)) / float64(v.memTotal)
	}
	return tea.Batch(a.animate("cpu", s.cpu), a.animate("mem", s.mem))
}

func (a *app) applyIfaces(v []iface, err error) {
	s := &a.s
	if err != nil {
		s.ifaces.set(nil, err)
		return
	}
	counters, _ := readNetDev("/proc/net/dev")
	now := time.Now()
	if s.ifHist == nil {
		s.ifHist = map[string][]float64{}
	}
	dt := now.Sub(s.ifAt).Seconds()
	for i := range v {
		c, ok := counters[v[i].name]
		p, had := s.ifPrev[v[i].name]
		if ok && had && dt > 0 && !s.ifAt.IsZero() {
			v[i].rxRate = float64(c.rx-min(c.rx, p.rx)) / dt
			v[i].txRate = float64(c.tx-min(c.tx, p.tx)) / dt
			s.ifHist[v[i].name] = push(s.ifHist[v[i].name], v[i].rxRate+v[i].txRate, 60)
		}
	}
	s.ifPrev, s.ifAt = counters, now
	s.ifaces.set(v, nil)
}

func (a *app) containers() []container {
	var out []container
	for _, q := range a.s.pods.val.quadlets {
		c := container{q: q}
		c.ps, c.exists = a.s.pods.val.ps[q.name]
		c.running = c.exists && c.ps.State == "running"
		c.unit = a.s.userUnits.val.units[q.unit]
		c.stats, c.hasStat = a.s.stats.val[q.name]
		if c.exists {
			c.image = a.s.images.val[c.ps.ImageID]
		}
		c.pending = a.s.pending.val[q.name]
		out = append(out, c)
	}
	return out
}

func (a *app) unit(name string) unit {
	if u, ok := a.s.units.val.units[name]; ok {
		return u
	}
	return a.s.userUnits.val.units[name]
}
