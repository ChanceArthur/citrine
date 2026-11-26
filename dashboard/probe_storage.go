package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type mount struct {
	point  string
	source string
	fstype string
	opts   string
}

func readMounts(path string) []mount {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []mount
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		pre, post, ok := strings.Cut(sc.Text(), " - ")
		if !ok {
			continue
		}
		a, b := strings.Fields(pre), strings.Fields(post)
		if len(a) < 6 || len(b) < 3 {
			continue
		}
		out = append(out, mount{point: unescape(a[4]), source: b[1], fstype: b[0], opts: a[5] + "," + b[2]})
	}
	return out
}

func unescape(s string) string {
	r := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return r.Replace(s)
}

func findMount(mounts []mount, point string) (mount, bool) {
	var found mount
	ok := false
	for _, m := range mounts {
		if m.point == point {
			found, ok = m, true
		}
	}
	return found, ok
}

type poolDevice struct {
	devid   int
	path    string
	size    uint64
	used    uint64
	missing bool
	errors  map[string]uint64
}

func (d poolDevice) errorCount() uint64 {
	var n uint64
	for _, v := range d.errors {
		n += v
	}
	return n
}

type allocation struct {
	kind    string
	profile string
	total   uint64
	used    uint64
}

type pool struct {
	mounted  bool
	degraded bool
	label    string
	uuid     string
	point    string
	size     uint64
	free     uint64
	devices  []poolDevice
	alloc    []allocation
	detailed bool
	detail   error
}

func (p pool) used() uint64 { return p.size - min(p.size, p.free) }

func (p pool) frac() float64 {
	if p.size == 0 {
		return 0
	}
	return float64(p.used()) / float64(p.size)
}

func (p pool) missing() int {
	n := 0
	for _, d := range p.devices {
		if d.missing {
			n++
		}
	}
	return n
}

func (p pool) errorCount() uint64 {
	var n uint64
	for _, d := range p.devices {
		n += d.errorCount()
	}
	return n
}

func (p pool) mirrored() bool {
	if len(p.alloc) == 0 {
		return false
	}
	for _, a := range p.alloc {
		if a.profile != "RAID1" {
			return false
		}
	}
	return true
}

func (p pool) health() (level, string) {
	switch {
	case !p.mounted:
		return lvBad, "not mounted"
	case p.degraded || p.missing() > 0:
		return lvBad, "degraded — a device is missing"
	case p.errorCount() > 0:
		return lvBad, plural(int(p.errorCount()), "device error", "device errors")
	case len(p.devices) < 2:
		return lvWarn, "one disk, no mirror"
	case !p.mirrored():
		return lvWarn, "not fully mirrored yet"
	}
	return lvOK, "mirrored"
}

func readPool(label, point string) (pool, error) {
	p := inspectPool(readMounts("/proc/self/mountinfo"), "/sys/fs/btrfs", label, point)
	if !p.mounted {
		return p, nil
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(point, &st); err == nil {
		p.size = st.Blocks * uint64(st.Bsize)
		p.free = st.Bavail * uint64(st.Bsize)
	}
	out, err := run(15*time.Second, look("pool")...)
	if err != nil {
		p.detail = err
		return p, nil
	}
	p.detailed = true
	mergeShow(&p, string(out))
	return p, nil
}

func inspectPool(mounts []mount, sysRoot, label, point string) pool {
	p := pool{label: label, point: point}
	m, ok := findMount(mounts, point)
	if !ok || m.fstype != "btrfs" {
		return p
	}
	p.mounted = true
	p.degraded = strings.Contains(","+m.opts+",", ",degraded,")
	if sys := findBtrfs(sysRoot, label); sys != "" {
		p.uuid = filepath.Base(sys)
		p.alloc = readAllocation(sys)
		p.devices = readDevinfo(sys)
	}
	return p
}

func findBtrfs(root, label string) string {
	dirs, _ := filepath.Glob(filepath.Join(root, "*", "label"))
	for _, l := range dirs {
		if readString(l) == label {
			return filepath.Dir(l)
		}
	}
	return ""
}

var profiles = []string{"raid1c4", "raid1c3", "raid10", "raid1", "raid0", "raid5", "raid6", "dup", "single"}

func readAllocation(sys string) []allocation {
	var out []allocation
	for _, kind := range []string{"data", "metadata", "system"} {
		dir := filepath.Join(sys, "allocation", kind)
		a := allocation{kind: kind}
		a.total, _ = strconv.ParseUint(readString(filepath.Join(dir, "total_bytes")), 10, 64)
		a.used, _ = strconv.ParseUint(readString(filepath.Join(dir, "bytes_used")), 10, 64)
		var found []string
		for _, prof := range profiles {
			if _, err := os.Stat(filepath.Join(dir, prof)); err != nil {
				continue
			}
			if prof != "single" {
				prof = strings.ToUpper(prof)
			}
			found = append(found, prof)
		}
		if len(found) == 0 {
			continue
		}
		a.profile = strings.Join(found, "+")
		out = append(out, a)
	}
	return out
}

func readDevinfo(sys string) []poolDevice {
	dirs, _ := filepath.Glob(filepath.Join(sys, "devinfo", "*"))
	var out []poolDevice
	for _, d := range dirs {
		id, err := strconv.Atoi(filepath.Base(d))
		if err != nil {
			continue
		}
		dev := poolDevice{devid: id, missing: readString(filepath.Join(d, "missing")) == "1", errors: map[string]uint64{}}
		for l := range strings.SplitSeq(readString(filepath.Join(d, "error_stats")), "\n") {
			f := strings.Fields(l)
			if len(f) == 2 {
				dev.errors[f[0]], _ = strconv.ParseUint(f[1], 10, 64)
			}
		}
		out = append(out, dev)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].devid < out[j].devid })
	return out
}

var devidRe = regexp.MustCompile(`devid\s+(\d+)\s+size\s+(\d+)\s+used\s+(\d+)\s+path\s+(.+)$`)

func mergeShow(p *pool, out string) {
	shown := map[int]poolDevice{}
	for l := range strings.SplitSeq(out, "\n") {
		m := devidRe.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		d := poolDevice{path: strings.TrimSpace(m[4]), errors: map[string]uint64{}}
		d.devid, _ = strconv.Atoi(m[1])
		d.size, _ = strconv.ParseUint(m[2], 10, 64)
		d.used, _ = strconv.ParseUint(m[3], 10, 64)
		d.missing = strings.Contains(strings.ToLower(d.path), "missing")
		shown[d.devid] = d
	}
	for i := range p.devices {
		d := &p.devices[i]
		if s, ok := shown[d.devid]; ok {
			d.path, d.size, d.used = s.path, s.size, s.used
			d.missing = d.missing || s.missing
			delete(shown, d.devid)
		}
	}
	for _, s := range shown {
		p.devices = append(p.devices, s)
	}
	sort.Slice(p.devices, func(i, j int) bool { return p.devices[i].devid < p.devices[j].devid })
}

type scrub struct {
	status   string
	started  time.Time
	duration string
	left     string
	progress float64
	errors   string
}

func readScrub() (scrub, error) {
	out, err := run(15*time.Second, look("scrub")...)
	if err != nil {
		return scrub{}, err
	}
	return parseScrub(string(out)), nil
}

var pctRe = regexp.MustCompile(`\(([\d.]+)%\)`)

func parseScrub(out string) scrub {
	var s scrub
	for l := range strings.SplitSeq(out, "\n") {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "Status":
			s.status = v
		case "Scrub started":
			s.started, _ = time.ParseInLocation("Mon Jan _2 15:04:05 2006", v, time.Local)
		case "Duration":
			s.duration = v
		case "Time left":
			s.left = v
		case "Bytes scrubbed":
			if m := pctRe.FindStringSubmatch(v); m != nil {
				f, _ := strconv.ParseFloat(m[1], 64)
				s.progress = f / 100
			}
		case "Error summary":
			s.errors = v
		}
	}
	if strings.Contains(out, "no stats available") {
		s.status = "never run"
	}
	return s
}

type disk struct {
	name     string
	path     string
	size     uint64
	model    string
	serial   string
	tran     string
	rota     bool
	rm       bool
	labels   []string
	fstypes  []string
	mounts   []string
	children int
	role     string
	pool     bool
	backup   bool
	system   bool
}

type lsblkDev struct {
	Name        string     `json:"name"`
	Path        string     `json:"path"`
	Size        flexNum    `json:"size"`
	Type        string     `json:"type"`
	Tran        *string    `json:"tran"`
	RM          flexBool   `json:"rm"`
	Rota        flexBool   `json:"rota"`
	Model       *string    `json:"model"`
	Serial      *string    `json:"serial"`
	Label       *string    `json:"label"`
	FSType      *string    `json:"fstype"`
	Mountpoints []*string  `json:"mountpoints"`
	Children    []lsblkDev `json:"children"`
}

type flexBool bool

func (b *flexBool) UnmarshalJSON(d []byte) error {
	s := strings.Trim(string(d), `"`)
	*b = flexBool(s == "true" || s == "1")
	return nil
}

type flexNum uint64

func (n *flexNum) UnmarshalJSON(d []byte) error {
	s := strings.Trim(string(d), `"`)
	if s == "null" || s == "" {
		return nil
	}
	v, err := strconv.ParseUint(s, 10, 64)
	*n = flexNum(v)
	return err
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

func readDisks(poolLabel, backupLabel string) ([]disk, error) {
	out, err := run(10*time.Second, "lsblk", "-J", "-b", "-o", "NAME,PATH,SIZE,TYPE,TRAN,RM,ROTA,MODEL,SERIAL,LABEL,FSTYPE,MOUNTPOINTS")
	if err != nil {
		return nil, err
	}
	return parseDisks(out, poolLabel, backupLabel)
}

var systemMounts = map[string]bool{"/": true, "/sysroot": true, "/var": true, "/boot": true, "/boot/efi": true, "/usr": true}

func parseDisks(b []byte, poolLabel, backupLabel string) ([]disk, error) {
	var raw struct {
		Blockdevices []lsblkDev `json:"blockdevices"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	var out []disk
	for _, d := range raw.Blockdevices {
		if d.Type != "disk" || strings.HasPrefix(d.Name, "zram") || strings.HasPrefix(d.Name, "loop") {
			continue
		}
		dk := disk{
			name: d.Name, path: d.Path, size: uint64(d.Size), model: clean(str(d.Model)), serial: clean(str(d.Serial)),
			tran: str(d.Tran), rota: bool(d.Rota), rm: bool(d.RM), children: len(d.Children),
		}
		var walk func(n lsblkDev)
		walk = func(n lsblkDev) {
			if l := clean(str(n.Label)); l != "" {
				dk.labels = append(dk.labels, l)
				dk.pool = dk.pool || (l == poolLabel && str(n.FSType) == "btrfs")
				dk.backup = dk.backup || l == backupLabel
			}
			if f := str(n.FSType); f != "" {
				dk.fstypes = append(dk.fstypes, f)
			}
			for _, mp := range n.Mountpoints {
				if m := str(mp); m != "" {
					dk.mounts = append(dk.mounts, m)
					dk.system = dk.system || systemMounts[m]
				}
			}
			for _, c := range n.Children {
				walk(c)
			}
		}
		walk(d)
		switch {
		case dk.system && dk.pool:
			dk.role = "system · pool"
		case dk.system:
			dk.role = "system"
		case dk.pool:
			dk.role = "pool"
		case dk.backup:
			dk.role = "backup"
		case dk.children > 0 || len(dk.fstypes) > 0:
			dk.role = "has data"
		default:
			dk.role = "blank"
		}
		out = append(out, dk)
	}
	return out, nil
}

func (d disk) adoptable() bool {
	return !d.system && !d.pool && !d.backup && !strings.HasPrefix(d.name, "sr")
}

type smart struct {
	ok      bool
	known   bool
	temp    int
	hours   int
	wear    int
	realloc int
	err     error
}

func readSmart(disks []disk) map[string]smart {
	out := map[string]smart{}
	for _, d := range disks {
		b, err := run(20*time.Second, look("smart", d.name)...)
		if len(b) == 0 && err != nil {
			out[d.name] = smart{err: err}
			continue
		}
		out[d.name] = parseSmart(b)
	}
	return out
}

func parseSmart(b []byte) smart {
	var raw struct {
		SmartStatus *struct {
			Passed bool `json:"passed"`
		} `json:"smart_status"`
		Temperature struct {
			Current int `json:"current"`
		} `json:"temperature"`
		PowerOnTime struct {
			Hours int `json:"hours"`
		} `json:"power_on_time"`
		NVMe struct {
			PercentageUsed int `json:"percentage_used"`
		} `json:"nvme_smart_health_information_log"`
		ATA struct {
			Table []struct {
				ID  int `json:"id"`
				Raw struct {
					Value int `json:"value"`
				} `json:"raw"`
			} `json:"table"`
		} `json:"ata_smart_attributes"`
		Smartctl struct {
			Messages []struct {
				String string `json:"string"`
			} `json:"messages"`
		} `json:"smartctl"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return smart{err: err}
	}
	s := smart{temp: raw.Temperature.Current, hours: raw.PowerOnTime.Hours, wear: raw.NVMe.PercentageUsed}
	if raw.SmartStatus != nil {
		s.known, s.ok = true, raw.SmartStatus.Passed
	} else if len(raw.Smartctl.Messages) > 0 {
		s.err = errors.New(raw.Smartctl.Messages[0].String)
	}
	for _, a := range raw.ATA.Table {
		if a.ID == 5 {
			s.realloc = a.Raw.Value
		}
	}
	return s
}
