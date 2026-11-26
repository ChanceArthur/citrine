package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type deployment struct {
	id        string
	osname    string
	checksum  string
	serial    int
	version   string
	imageRef  string
	digest    string
	timestamp time.Time
	booted    bool
	staged    bool
	pinned    bool
	citrine   release
}

type ostreeStatus struct {
	deployments []deployment
	busy        bool
	update      string
}

func rpmOstreeStatus() (ostreeStatus, error) {
	b, err := run(30*time.Second, "rpm-ostree", "status", "--json")
	if err != nil {
		return ostreeStatus{}, err
	}
	st, err := parseOstree(b)
	if err != nil {
		return st, err
	}
	for i := range st.deployments {
		d := &st.deployments[i]
		if d.booted {
			d.citrine = readRelease(releasePath)
			continue
		}
		root := filepath.Join("/ostree/deploy", d.osname, "deploy", fmt.Sprintf("%s.%d", d.checksum, d.serial))
		d.citrine = readRelease(filepath.Join(root, releasePath))
	}
	return st, nil
}

func parseOstree(b []byte) (ostreeStatus, error) {
	var raw struct {
		Deployments []struct {
			ID        string `json:"id"`
			OSName    string `json:"osname"`
			Checksum  string `json:"checksum"`
			Serial    int    `json:"serial"`
			Version   string `json:"version"`
			Origin    string `json:"origin"`
			ImageRef  string `json:"container-image-reference"`
			Digest    string `json:"container-image-reference-digest"`
			Timestamp int64  `json:"timestamp"`
			Booted    bool   `json:"booted"`
			Staged    bool   `json:"staged"`
			Pinned    bool   `json:"pinned"`
		} `json:"deployments"`
		Transaction  json.RawMessage            `json:"transaction"`
		CachedUpdate map[string]json.RawMessage `json:"cached-update"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return ostreeStatus{}, err
	}
	var st ostreeStatus
	for _, d := range raw.Deployments {
		ref := d.ImageRef
		if ref == "" {
			ref = d.Origin
		}
		st.deployments = append(st.deployments, deployment{
			id: d.ID, osname: d.OSName, checksum: d.Checksum, serial: d.Serial,
			version: d.Version, imageRef: ref, digest: d.Digest,
			timestamp: time.Unix(d.Timestamp, 0), booted: d.Booted, staged: d.Staged, pinned: d.Pinned,
		})
	}
	st.busy = len(raw.Transaction) > 0 && string(raw.Transaction) != "null"
	if v, ok := raw.CachedUpdate["version"]; ok {
		_ = json.Unmarshal(v, &st.update)
	}
	return st, nil
}

func (s ostreeStatus) booted() (deployment, bool) {
	for _, d := range s.deployments {
		if d.booted {
			return d, true
		}
	}
	return deployment{}, false
}

func (s ostreeStatus) staged() (deployment, bool) {
	for _, d := range s.deployments {
		if d.staged {
			return d, true
		}
	}
	return deployment{}, false
}

func (s ostreeStatus) rollback() (deployment, bool) {
	seenBooted := false
	for _, d := range s.deployments {
		if d.booted {
			seenBooted = true
			continue
		}
		if seenBooted && !d.staged {
			return d, true
		}
	}
	return deployment{}, false
}

func (d deployment) tag() string {
	ref := d.imageRef
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		return ref[i+1:]
	}
	return ""
}

func (d deployment) image() string {
	ref := strings.TrimPrefix(d.imageRef, "ostree-unverified-registry:")
	return strings.TrimPrefix(ref, "ostree-image-signed:docker://")
}

func (d deployment) name() string {
	if d.citrine.version != "" {
		return d.citrine.version
	}
	if versionRe.MatchString(d.version) {
		return d.version
	}
	if t := d.tag(); t != "" && t != "latest" {
		return t
	}
	if d.digest != "" {
		return shortDigest(d.digest)
	}
	return d.version
}

func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

var versionRe = regexp.MustCompile(`^\d{2}\.\d{2}\.\d{2}\.\d{6}$`)

func listVersions(repo string) ([]string, error) {
	b, err := run(45*time.Second, "skopeo", "list-tags", "docker://"+repo)
	if err != nil {
		return nil, err
	}
	return parseTags(b)
}

func parseTags(b []byte) ([]string, error) {
	var raw struct {
		Tags []string `json:"Tags"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	var out []string
	for _, t := range raw.Tags {
		if versionRe.MatchString(t) {
			out = append(out, t)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out, nil
}
