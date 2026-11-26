package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

type quadlet struct {
	unit        string
	name        string
	description string
	impact      string
	image       string
	autoUpdate  string
	ports       []string
	networks    []string
	health      bool
}

func readQuadlets(dir string) ([]quadlet, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.container"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		if _, err := os.Stat(dir); err != nil {
			return nil, err
		}
	}
	var out []quadlet
	for _, p := range paths {
		q, err := parseQuadlet(p)
		if err != nil {
			continue
		}
		out = append(out, q)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func parseQuadlet(path string) (quadlet, error) {
	f, err := os.Open(path)
	if err != nil {
		return quadlet{}, err
	}
	defer f.Close()
	base := strings.TrimSuffix(filepath.Base(path), ".container")
	q := quadlet{unit: base + ".service", name: "systemd-" + base}
	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[]")
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = clean(v)
		switch section + "." + k {
		case "Unit.Description":
			q.description = v
		case "Container.ContainerName":
			q.name = v
		case "Container.Image":
			q.image = v
		case "Container.AutoUpdate":
			q.autoUpdate = v
		case "Container.PublishPort":
			q.ports = append(q.ports, v)
		case "Container.Network":
			q.networks = append(q.networks, strings.TrimSuffix(v, ".network"))
		case "Container.HealthCmd":
			q.health = true
		}
	}
	return q, sc.Err()
}

type psEntry struct {
	ID        string `json:"Id"`
	Names     []string
	Image     string
	ImageID   string
	State     string
	Status    string
	StartedAt int64
	ExitedAt  int64
	ExitCode  int32
	Restarts  uint
	Labels    map[string]string
	Networks  []string
}

var healthRe = regexp.MustCompile(`\((healthy|unhealthy|starting)\)\s*$`)

func (p psEntry) health() string {
	if m := healthRe.FindStringSubmatch(p.Status); m != nil {
		return m[1]
	}
	return ""
}

func (p psEntry) name() string {
	if len(p.Names) > 0 {
		return p.Names[0]
	}
	return p.ID
}

func podmanPS() (map[string]psEntry, error) {
	b, err := run(15*time.Second, "podman", "ps", "--all", "--format", "json")
	if err != nil {
		return nil, err
	}
	return parsePS(b)
}

func parsePS(b []byte) (map[string]psEntry, error) {
	var list []psEntry
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	out := map[string]psEntry{}
	for _, p := range list {
		out[p.name()] = p
	}
	return out, nil
}

type ctrStats struct {
	cpu    float64
	mem    uint64
	memPct float64
	pids   int
}

func podmanStats() (map[string]ctrStats, error) {
	b, err := run(20*time.Second, "podman", "stats", "--no-stream", "--format", "json")
	if err != nil {
		return nil, err
	}
	return parseStats(b)
}

func parseStats(b []byte) (map[string]ctrStats, error) {
	var list []struct {
		Name    string `json:"name"`
		CPU     string `json:"cpu_percent"`
		Mem     string `json:"mem_usage"`
		MemPerc string `json:"mem_percent"`
		Pids    string `json:"pids"`
	}
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	out := map[string]ctrStats{}
	for _, s := range list {
		used, _, _ := strings.Cut(s.Mem, "/")
		st := ctrStats{
			cpu:    percent(s.CPU),
			mem:    humanBytes(strings.TrimSpace(used)),
			memPct: percent(s.MemPerc),
		}
		st.pids, _ = strconv.Atoi(strings.TrimSpace(s.Pids))
		out[s.Name] = st
	}
	return out, nil
}

func percent(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "%"), 64)
	if err != nil {
		return 0
	}
	return f / 100
}

func humanBytes(s string) uint64 {
	s = strings.TrimSpace(s)
	i := strings.IndexFunc(s, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	if i < 0 {
		i = len(s)
	}
	f, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0
	}
	mult := map[string]float64{
		"": 1, "b": 1,
		"kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12,
		"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40,
	}[strings.ToLower(strings.TrimSpace(s[i:]))]
	if mult == 0 {
		mult = 1
	}
	return uint64(f * mult)
}

type imageInfo struct {
	id      string
	names   []string
	created time.Time
	digest  string
	size    uint64
}

func podmanImages() (map[string]imageInfo, error) {
	b, err := run(20*time.Second, "podman", "images", "--format", "json")
	if err != nil {
		return nil, err
	}
	return parseImages(b)
}

func parseImages(b []byte) (map[string]imageInfo, error) {
	var list []struct {
		ID      string `json:"Id"`
		Names   []string
		Created int64
		Digest  string
		Size    int64
	}
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	out := map[string]imageInfo{}
	for _, im := range list {
		cur := out[im.ID]
		cur.id = im.ID
		cur.created = time.Unix(im.Created, 0)
		cur.digest = im.Digest
		cur.size = uint64(max(0, im.Size))
		for _, n := range im.Names {
			if !slices.Contains(cur.names, n) {
				cur.names = append(cur.names, n)
			}
		}
		out[im.ID] = cur
	}
	return out, nil
}

func hasTag(images map[string]imageInfo, ref string) bool {
	for _, im := range images {
		if slices.Contains(im.names, ref) {
			return true
		}
	}
	return false
}

func podmanPending() (map[string]string, error) {
	b, err := run(2*time.Minute, "podman", "auto-update", "--dry-run", "--format", "json")
	if err != nil {
		return nil, err
	}
	return parsePending(b)
}

func readPending(dir string) (map[string]string, error) {
	out, err := podmanPending()
	if err != nil {
		return nil, err
	}
	qs, _ := readQuadlets(dir)
	for _, q := range qs {
		if q.autoUpdate != "" || strings.Contains(q.image, "@") {
			continue
		}
		raw, err := run(30*time.Second, "skopeo", "inspect", "--raw", "docker://"+q.image)
		if err != nil {
			continue
		}
		b, _ := run(10*time.Second, "podman", "image", "inspect", "--format", "{{json .RepoDigests}}", q.image)
		var local []string
		_ = json.Unmarshal(b, &local)
		out[q.name] = "pending"
		if upToDate(raw, local) {
			out[q.name] = "false"
		}
	}
	return out, nil
}

func upToDate(manifest []byte, repoDigests []string) bool {
	remote := []string{fmt.Sprintf("sha256:%x", sha256.Sum256(manifest))}
	var list struct {
		Manifests []struct {
			Digest   string
			Platform struct{ Architecture, OS string }
		}
	}
	if json.Unmarshal(manifest, &list) == nil {
		for _, m := range list.Manifests {
			if m.Platform.OS == "linux" && m.Platform.Architecture == runtime.GOARCH {
				remote = append(remote, m.Digest)
			}
		}
	}
	for _, d := range repoDigests {
		for _, r := range remote {
			if strings.HasSuffix(d, "@"+r) {
				return true
			}
		}
	}
	return false
}

func parsePending(b []byte) (map[string]string, error) {
	var list []struct {
		ContainerName string
		Updated       string
	}
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, r := range list {
		out[r.ContainerName] = r.Updated
	}
	return out, nil
}

type container struct {
	q       quadlet
	ps      psEntry
	running bool
	exists  bool
	unit    unit
	stats   ctrStats
	hasStat bool
	image   imageInfo
	pending string
}

func (c container) state() (level, string) {
	u := c.unit
	switch {
	case c.running && c.ps.health() == "unhealthy":
		return lvBad, "unhealthy"
	case c.running && c.ps.health() == "starting":
		return lvWarn, "starting"
	case c.running && c.ps.health() == "healthy":
		return lvOK, "healthy"
	case c.running:
		return lvOK, "running"
	case u.active == "activating":
		return lvWarn, "starting"
	case u.active == "deactivating":
		return lvWarn, "stopping"
	case u.active == "failed":
		return lvBad, "failed"
	case c.exists && c.ps.State != "":
		return lvBad, c.ps.State
	}
	return lvBad, "stopped"
}

func (c container) up() time.Duration {
	if !c.running || c.ps.StartedAt == 0 {
		return 0
	}
	return time.Since(time.Unix(c.ps.StartedAt, 0))
}

func shortImage(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		ref = ref[i+1:]
	}
	return ref
}
