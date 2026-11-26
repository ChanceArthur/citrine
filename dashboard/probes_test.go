package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const lsblkOut = `{ "blockdevices": [
 { "name": "sda", "path": "/dev/sda", "size": 1000204886016, "type": "disk", "tran": "usb", "rm": false, "rota": true, "model": null, "serial": "0000000000000000", "label": "citrine-data", "fstype": "btrfs", "mountpoints": [] },
 { "name": "sdb", "path": "/dev/sdb", "size": 2000398934016, "type": "disk", "tran": "usb", "rm": false, "rota": true, "model": "Elements 25A3", "serial": "X1", "label": null, "fstype": null, "mountpoints": [] },
 { "name": "sdc", "path": "/dev/sdc", "size": 500107862016, "type": "disk", "tran": "usb", "rm": true, "rota": false, "model": null, "serial": null, "label": null, "fstype": null, "mountpoints": [], "children": [
  { "name": "sdc1", "path": "/dev/sdc1", "size": 500106813440, "type": "part", "tran": "usb", "rm": true, "rota": false, "model": null, "serial": null, "label": "CITRINE-BK", "fstype": "exfat", "mountpoints": [] } ] },
 { "name": "sdd", "path": "/dev/sdd", "size": 64000000000, "type": "disk", "tran": "usb", "rm": true, "rota": false, "model": null, "serial": null, "label": null, "fstype": null, "mountpoints": [], "children": [
  { "name": "sdd1", "path": "/dev/sdd1", "size": 63999000000, "type": "part", "tran": "usb", "rm": true, "rota": false, "model": null, "serial": null, "label": "PHOTOS", "fstype": "vfat", "mountpoints": [] } ] },
 { "name": "zram0", "path": "/dev/zram0", "size": 4294967296, "type": "disk", "tran": null, "rm": false, "rota": false, "model": null, "serial": null, "label": "zram0", "fstype": "swap", "mountpoints": [ "[SWAP]" ] },
 { "name": "nvme0n1", "path": "/dev/nvme0n1", "size": 1024209543168, "type": "disk", "tran": "nvme", "rm": false, "rota": false, "model": "NVME SSD 1TB", "serial": "S0", "label": null, "fstype": null, "mountpoints": [], "children": [
  { "name": "nvme0n1p1", "path": "/dev/nvme0n1p1", "size": 1048576, "type": "part", "tran": "nvme", "rm": false, "rota": false, "model": null, "serial": null, "label": null, "fstype": null, "mountpoints": [] },
  { "name": "nvme0n1p2", "path": "/dev/nvme0n1p2", "size": 133169152, "type": "part", "tran": "nvme", "rm": false, "rota": false, "model": null, "serial": null, "label": "EFI-SYSTEM", "fstype": "vfat", "mountpoints": [] },
  { "name": "nvme0n1p3", "path": "/dev/nvme0n1p3", "size": 402653184, "type": "part", "tran": "nvme", "rm": false, "rota": false, "model": null, "serial": null, "label": "boot", "fstype": "ext4", "mountpoints": [ "/boot" ] },
  { "name": "nvme0n1p4", "path": "/dev/nvme0n1p4", "size": 25769803776, "type": "part", "tran": "nvme", "rm": false, "rota": false, "model": null, "serial": null, "label": "root", "fstype": "xfs", "mountpoints": [ "/sysroot/ostree/deploy/fedora-coreos/var", "/sysroot", "/etc" ] },
  { "name": "nvme0n1p5", "path": "/dev/nvme0n1p5", "size": 68719476736, "type": "part", "tran": "nvme", "rm": false, "rota": false, "model": null, "serial": null, "label": "citrine-var", "fstype": "btrfs", "mountpoints": [ "/var" ] },
  { "name": "nvme0n1p6", "path": "/dev/nvme0n1p6", "size": 929181295104, "type": "part", "tran": "nvme", "rm": false, "rota": false, "model": null, "serial": null, "label": "citrine-data", "fstype": "btrfs", "mountpoints": [ "/var/mnt/data" ] } ] }
] }`

func TestDisksTakeTheirRolesFromLsblk(t *testing.T) {
	disks, err := parseDisks([]byte(lsblkOut), "citrine-data", "CITRINE-BK")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name, role string
		adoptable  bool
	}{
		{"sda", "pool", false},
		{"sdb", "blank", true},
		{"sdc", "backup", false},
		{"sdd", "has data", true},
		{"nvme0n1", "system · pool", false},
	}
	if len(disks) != len(want) {
		t.Fatalf("got %d disks, want %d: zram is no disk to offer", len(disks), len(want))
	}
	for i, w := range want {
		d := disks[i]
		if d.name != w.name || d.role != w.role || d.adoptable() != w.adoptable {
			t.Errorf("%s: role %q adoptable %v, want %q %v", d.name, d.role, d.adoptable(), w.role, w.adoptable)
		}
	}
}

const showOut = `Label: 'citrine-data'  uuid: 00000000-0000-4000-8000-000000000000
	Total devices 2 FS bytes used 7448584192
	devid    1 size 929181294592 used 12918456320 path /dev/nvme0n1p6
	devid    2 size 1000204886016 used 12918456320 path /dev/sda
`

func TestThePoolsDevicesComeFromFilesystemShow(t *testing.T) {
	p := pool{devices: []poolDevice{{devid: 1, errors: map[string]uint64{}}, {devid: 2, errors: map[string]uint64{}}}}
	mergeShow(&p, showOut)
	if len(p.devices) != 2 || p.devices[0].path != "/dev/nvme0n1p6" || p.devices[1].path != "/dev/sda" {
		t.Fatalf("devices: %+v", p.devices)
	}
	if p.devices[1].size != 1000204886016 || p.devices[1].used != 12918456320 || p.missing() != 0 {
		t.Errorf("second device: %+v", p.devices[1])
	}

	gone := pool{devices: []poolDevice{{devid: 1, errors: map[string]uint64{}}, {devid: 2, missing: true, errors: map[string]uint64{}}}}
	mergeShow(&gone, "\tdevid    1 size 929181294592 used 12918456320 path /dev/nvme0n1p6\n")
	if gone.missing() != 1 {
		t.Errorf("a device sysfs calls missing stays missing when show leaves it out: %+v", gone.devices)
	}
	gone.mounted = true
	if lv, _ := gone.health(); lv != lvBad {
		t.Errorf("a pool short a device reads as %v", lv)
	}
}

func TestScrubStatusIsRead(t *testing.T) {
	done := parseScrub(`UUID:             00000000-0000-4000-8000-000000000000
Scrub started:    Wed Nov  4 03:54:45 2026
Status:           finished
Duration:         0:03:18
Total to scrub:   13.87GiB
Rate:             66.04MiB/s
Error summary:    no errors found
`)
	if done.status != "finished" || done.duration != "0:03:18" || done.errors != "no errors found" {
		t.Errorf("finished scrub: %+v", done)
	}
	if want := time.Date(2026, 11, 4, 3, 54, 45, 0, time.Local); !done.started.Equal(want) {
		t.Errorf("started %v, want %v", done.started, want)
	}

	running := parseScrub(`Scrub started:    Wed Nov  4 03:54:45 2026
Status:           running
Duration:         0:01:00
Time left:        0:02:18
Bytes scrubbed:   4.16GiB  (30.00%)
Error summary:    no errors found
`)
	if running.status != "running" || running.left != "0:02:18" || running.progress < 0.299 || running.progress > 0.301 {
		t.Errorf("running scrub: %+v", running)
	}

	if never := parseScrub("scrub status for 00000000\n\tno stats available\n"); never.status != "never run" {
		t.Errorf("a pool never scrubbed reads as %q", never.status)
	}
}

const unitsOut = `Id=citrine-backup.service
Triggers=
Description=Back up the databases and chosen paths to R2
LoadState=loaded
ActiveState=inactive
SubState=dead
UnitFileState=static
StateChangeTimestamp=
Result=success
NRestarts=0
ExecMainExitTimestamp=
ExecMainStatus=0

Id=citrine-backup.timer
Triggers=citrine-backup.service
Description=Nightly off-site backup
LoadState=loaded
ActiveState=active
SubState=waiting
UnitFileState=enabled
StateChangeTimestamp=@1794708826
NextElapseUSecRealtime=@1794726244
LastTriggerUSec=@1794641158
Result=success

Id=citrine-nope.service
Triggers=
Description=citrine-nope.service
LoadState=not-found
ActiveState=inactive
SubState=dead
UnitFileState=
StateChangeTimestamp=
Result=success
NRestarts=0
ExecMainExitTimestamp=
ExecMainStatus=0
`

func TestUnitsAreReadFromSystemctlShow(t *testing.T) {
	units := parseShow(unitsOut, false)
	if len(units) != 3 {
		t.Fatalf("got %d units", len(units))
	}
	timer := units["citrine-backup.timer"]
	if timer.triggers != "citrine-backup.service" || timer.next.Unix() != 1794726244 || timer.last.Unix() != 1794641158 {
		t.Errorf("timer: %+v", timer)
	}
	if _, s := timer.state(); s != "waiting" {
		t.Errorf("a waiting timer reads as %q", s)
	}
	svc := units["citrine-backup.service"]
	if !svc.since.IsZero() || !svc.exited.IsZero() {
		t.Errorf("an empty timestamp is no time: %+v", svc)
	}
	if _, s := svc.state(); s != "idle" {
		t.Errorf("a service that hasn't run this boot reads as %q", s)
	}
	if _, s := units["citrine-nope.service"].state(); s != "not installed" {
		t.Errorf("a unit systemd doesn't have reads as %q", s)
	}
	if got := parseShow("Id=x.service\nDescription=a\x1b]0;b\x07c\n", false)["x.service"].description; got != "ac" {
		t.Errorf("a description's escape sequence survives: %q", got)
	}
}

func TestContainersAreReadFromPodman(t *testing.T) {
	ps, err := parsePS([]byte(`[{"Id":"c811d09f2066","Names":["web"],"ImageID":"73c18bbc52a2","State":"running","Status":"Up 2 hours (healthy)","StartedAt":1794708835,"ExitedAt":-62135596800,"ExitCode":0,"Restarts":0,"Networks":["edge"]},
{"Id":"aa","Names":["db"],"State":"running","Status":"Up 2 hours","StartedAt":1794708835}]`))
	if err != nil {
		t.Fatal(err)
	}
	if ps["web"].health() != "healthy" || ps["db"].health() != "" {
		t.Errorf("health: web %q, db %q", ps["web"].health(), ps["db"].health())
	}

	stats, err := parseStats([]byte(`[{"name":"web","cpu_time":"41.846669s","cpu_percent":"0.50%","avg_cpu":"0.50%","mem_usage":"5.39MB / 13.5GB","mem_percent":"0.04%","net_io":"33.14MB / 17.37MB","block_io":"0B / 0B","pids":"8"}]`))
	if err != nil {
		t.Fatal(err)
	}
	s := stats["web"]
	if s.mem != 5390000 || s.pids != 8 || s.cpu < 0.0049 || s.cpu > 0.0051 {
		t.Errorf("stats: %+v", s)
	}
}

func TestThePoolIsFoundInMountinfo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mountinfo")
	line := "232 68 0:69 / /var/mnt/data rw,noatime shared:480 - btrfs /dev/nvme0n1p6 rw,seclabel,compress=zstd:1,discard=async,space_cache=v2,subvolid=5,subvol=/\n"
	if err := os.WriteFile(path, []byte("68 1 0:30 / /var rw shared:1 - btrfs /dev/nvme0n1p5 rw\n"+line), 0o600); err != nil {
		t.Fatal(err)
	}
	p := inspectPool(readMounts(path), t.TempDir(), "citrine-data", "/var/mnt/data")
	if !p.mounted || p.degraded {
		t.Errorf("a whole pool: %+v", p)
	}

	if err := os.WriteFile(path, []byte("232 68 0:69 / /var/mnt/data rw,noatime shared:480 - btrfs /dev/sda rw,degraded,compress=zstd:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if p := inspectPool(readMounts(path), t.TempDir(), "citrine-data", "/var/mnt/data"); !p.degraded {
		t.Errorf("a pool mounted degraded doesn't say so: %+v", p)
	}
	if p := inspectPool(readMounts(path), t.TempDir(), "citrine-data", "/var/mnt/elsewhere"); p.mounted {
		t.Error("a pool that isn't mounted reads as mounted")
	}
}
