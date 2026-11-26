package main

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"time"
)

type snapshot struct {
	ID      string    `json:"id"`
	ShortID string    `json:"short_id"`
	Time    time.Time `json:"time"`
	Tags    []string  `json:"tags"`
	Paths   []string  `json:"paths"`
	Summary *struct {
		DataAdded           uint64    `json:"data_added"`
		TotalFilesProcessed uint      `json:"total_files_processed"`
		TotalBytesProcessed uint64    `json:"total_bytes_processed"`
		BackupStart         time.Time `json:"backup_start"`
		BackupEnd           time.Time `json:"backup_end"`
	} `json:"summary"`
}

func (s snapshot) short() string {
	if s.ShortID != "" {
		return s.ShortID
	}
	if len(s.ID) > 8 {
		return s.ID[:8]
	}
	return s.ID
}

func r2Snapshots() ([]snapshot, error) {
	b, err := run(90*time.Second, look("snapshots")...)
	if err != nil {
		return nil, err
	}
	return parseSnapshots(b)
}

func parseSnapshots(b []byte) ([]snapshot, error) {
	var list []snapshot
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Time.After(list[j].Time) })
	return list, nil
}

func newestTagged(list []snapshot, tag string) (snapshot, bool) {
	for _, s := range list {
		if slices.Contains(s.Tags, tag) {
			return s, true
		}
	}
	return snapshot{}, false
}

type drive struct {
	last    time.Time
	mounted bool
	source  string
}

func readDrive(mountPoint string) (drive, error) {
	var d drive
	if v := readString(driveTag); v != "" {
		d.last, _ = time.Parse(time.RFC3339, v)
	}
	if m, ok := findMount(readMounts("/proc/self/mountinfo"), mountPoint); ok {
		d.mounted, d.source = true, m.source
	}
	return d, nil
}

func short(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 && len(msg)-i < 60 {
		msg = msg[i+2:]
	}
	return msg
}
