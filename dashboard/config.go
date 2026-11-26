package main

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultsPath = "/usr/lib/citrine/defaults.conf"
	confPath     = "/etc/citrine/citrine.conf"
	siteConf     = "/etc/citrine/site.conf"
	confReader   = "/usr/lib/citrine/conf.sh"
	dashboardEnv = "/etc/citrine/env/dashboard.env"
	releasePath  = "/usr/lib/citrine-release"
	stateDir     = "/var/lib/citrine"
	quadletRoot  = "/etc/containers/systemd/users"
	driveTag     = stateDir + "/last-drive-backup"
)

var (
	libexec      = "/usr/libexec/citrine"
	firstBootTag = stateDir + "/first-boot.done"
	setupTag     = stateDir + "/setup.done"
)

type config map[string]string

func loadConfig() config {
	out, err := exec.Command("env", "-i", "/bin/bash", "-c", "set -a && . "+defaultsPath+" && . "+confReader+" && conf_read "+confPath+" && for f in "+siteConf+" "+dashboardEnv+"; do if [ -r \"$f\" ]; then . \"$f\"; fi; done && exec env -0").Output()
	if err != nil {
		return config{}
	}
	c := config{}
	for kv := range bytes.SplitSeq(out, []byte{0}) {
		if k, v, ok := strings.Cut(string(kv), "="); ok {
			c[k] = v
		}
	}
	return c
}

func (c config) uid() int {
	n, err := strconv.Atoi(c["USER_UID"])
	if err != nil {
		return -1
	}
	return n
}

func (c config) quadlets() string {
	return filepath.Join(quadletRoot, c["USER_UID"])
}

func (c config) repo() string {
	img := c["IMAGE"]
	if i := strings.LastIndex(img, ":"); i > strings.LastIndex(img, "/") {
		return img[:i]
	}
	return img
}

type release struct {
	version string
	commit  string
}

func readRelease(path string) release {
	f, err := os.Open(path)
	if err != nil {
		return release{}
	}
	defer f.Close()
	var r release
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), "=")
		switch k {
		case "VERSION":
			r.version = v
		case "COMMIT":
			r.commit = v
		}
	}
	return r
}

func (r release) short() string {
	if len(r.commit) > 7 {
		return r.commit[:7]
	}
	return r.commit
}
