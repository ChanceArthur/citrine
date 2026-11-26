package main

import (
	"bufio"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type cpuTimes struct {
	idle, total uint64
}

type sysReading struct {
	at       time.Time
	cpu      cpuTimes
	cores    int
	memTotal uint64
	memAvail uint64
	load     [3]float64
	uptime   time.Duration
	temp     float64
	net      map[string]ifCounters
}

type ifCounters struct {
	rx, tx uint64
}

func readSystem() (sysReading, error) {
	r := sysReading{at: time.Now()}
	var err error
	if r.cpu, r.cores, err = readCPU("/proc/stat"); err != nil {
		return r, err
	}
	r.memTotal, r.memAvail, _ = readMem("/proc/meminfo")
	r.load, _ = readLoad("/proc/loadavg")
	r.uptime, _ = readUptime("/proc/uptime")
	r.temp = readTemp("/sys/class/hwmon")
	r.net, _ = readNetDev("/proc/net/dev")
	return r, nil
}

func readCPU(path string) (cpuTimes, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return cpuTimes{}, 0, err
	}
	defer f.Close()
	var t cpuTimes
	cores := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 || !strings.HasPrefix(fields[0], "cpu") {
			continue
		}
		if fields[0] != "cpu" {
			cores++
			continue
		}
		for i, v := range fields[1:] {
			n, _ := strconv.ParseUint(v, 10, 64)
			if i == 3 || i == 4 {
				t.idle += n
			}
			if i < 8 {
				t.total += n
			}
		}
	}
	if t.total == 0 {
		return t, cores, errors.New("no cpu line in " + path)
	}
	return t, cores, nil
}

func cpuBusy(prev, cur cpuTimes) float64 {
	dt := float64(cur.total - prev.total)
	if prev.total == 0 || cur.total <= prev.total || dt == 0 {
		return 0
	}
	return 1 - float64(cur.idle-prev.idle)/dt
}

func readMem(path string) (total, avail uint64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		n, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			total = n * 1024
		case "MemAvailable:":
			avail = n * 1024
		}
	}
	return total, avail, nil
}

func readLoad(path string) ([3]float64, error) {
	var l [3]float64
	b, err := os.ReadFile(path)
	if err != nil {
		return l, err
	}
	fields := strings.Fields(string(b))
	for i := 0; i < 3 && i < len(fields); i++ {
		l[i], _ = strconv.ParseFloat(fields[i], 64)
	}
	return l, nil
}

func readUptime(path string) (time.Duration, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0, errors.New("empty " + path)
	}
	s, err := strconv.ParseFloat(fields[0], 64)
	return time.Duration(s * float64(time.Second)), err
}

type sensor struct {
	rank int
	pkg  bool
	c    float64
}

func (a sensor) beats(b sensor) bool {
	if a.rank != b.rank {
		return a.rank > b.rank
	}
	if a.pkg != b.pkg {
		return a.pkg
	}
	return a.c > b.c
}

func readTemp(root string) float64 {
	dirs, _ := filepath.Glob(filepath.Join(root, "hwmon*"))
	best := sensor{rank: -1}
	for _, d := range dirs {
		rank := map[string]int{"coretemp": 3, "k10temp": 3, "zenpower": 3, "cpu_thermal": 2, "acpitz": 1}[readString(filepath.Join(d, "name"))]
		if rank == 0 {
			continue
		}
		inputs, _ := filepath.Glob(filepath.Join(d, "temp*_input"))
		for _, in := range inputs {
			milli, err := strconv.ParseFloat(readString(in), 64)
			if err != nil || milli <= 0 {
				continue
			}
			label := readString(strings.TrimSuffix(in, "_input") + "_label")
			s := sensor{rank: rank, c: milli / 1000}
			s.pkg = strings.HasPrefix(label, "Package") || strings.HasPrefix(label, "Tctl") || strings.HasPrefix(label, "Tdie")
			if s.beats(best) {
				best = s
			}
		}
	}
	return max(0, best.c)
}

func readNetDev(path string) (map[string]ifCounters, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]ifCounters{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(fields[0], 10, 64)
		tx, _ := strconv.ParseUint(fields[8], 10, 64)
		out[strings.TrimSpace(name)] = ifCounters{rx: rx, tx: tx}
	}
	return out, nil
}

func physical(name string) bool {
	_, err := os.Stat(filepath.Join("/sys/class/net", name, "device"))
	return err == nil
}

func readString(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func kernel() string {
	return readString("/proc/sys/kernel/osrelease")
}

type iface struct {
	name   string
	state  string
	mac    string
	speed  int
	addrs  []string
	phys   bool
	rxRate float64
	txRate float64
}

func readIfaces() ([]iface, error) {
	nis, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []iface
	for _, ni := range nis {
		if ni.Flags&net.FlagLoopback != 0 || virtual(ni.Name) {
			continue
		}
		it := iface{
			name:  ni.Name,
			state: linkState(readString(filepath.Join("/sys/class/net", ni.Name, "operstate")), ni.Flags),
			mac:   ni.HardwareAddr.String(),
			phys:  physical(ni.Name),
		}
		it.speed, _ = strconv.Atoi(readString(filepath.Join("/sys/class/net", ni.Name, "speed")))
		addrs, _ := ni.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLinkLocalUnicast() {
				it.addrs = append(it.addrs, a.String())
			}
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].phys && !out[j].phys })
	return out, nil
}

func linkState(oper string, flags net.Flags) string {
	if oper == "unknown" && flags&net.FlagRunning != 0 {
		return "up"
	}
	return oper
}

func virtual(name string) bool {
	for _, p := range []string{"veth", "podman", "cni", "docker", "br-"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
