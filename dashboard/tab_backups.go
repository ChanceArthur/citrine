package main

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

type backupsTab struct {
	sel, off int
}

var (
	kBackup  = bind("b", "back up now", "b")
	kRestore = bind("x", "restore databases", "x")
)

func (backupsTab) title() string { return "Backups" }

func (backupsTab) needs() []kind { return []kind{kUnits, kSnapshots, kDrive, kDisks} }

func (backupsTab) keys(*app) []key.Binding {
	return []key.Binding{kBackup, kRestore}
}

func (backupsTab) selects(msg tea.KeyPressMsg) bool { return key.Matches(msg, kSelect) }

func (b *backupsTab) key(a *app, msg tea.KeyPressMsg) tea.Cmd {
	n := len(a.s.snapshots.val)
	switch {
	case key.Matches(msg, kUp):
		b.sel = clamp(b.sel-1, 0, n-1)
	case key.Matches(msg, kDown):
		b.sel = clamp(b.sel+1, 0, n-1)
	case key.Matches(msg, kBackup):
		return a.confirm("Back up to R2 now?", "Dumps the databases and sends them and the rest of the nightly set to R2. The nightly run still happens.", "Back up", false, startCmd(spec{
			key: "backup", title: "Back up to R2", root: true,
			argv: systemctl(false, "start", "citrine-backup.service"), unit: "citrine-backup.service",
			done: "Backed up to R2", after: []kind{kSnapshots},
		}))
	case key.Matches(msg, kRestore):
		if a.s.drive.val.mounted {
			return a.choose("Restore from where?", "The drive's repository is readable while it's mounted.", []huh.Option[string]{
				huh.NewOption("R2 — the nightly backup", "r2"),
				huh.NewOption("The drive — "+a.cfg["BACKUP_MOUNT"], "drive"),
			}, func(src string) tea.Cmd { return b.restore(a, src) })
		}
		return b.restore(a, "r2")
	}
	return nil
}

func (backupsTab) restore(a *app, src string) tea.Cmd {
	if src == "" {
		return nil
	}
	newest := "the newest database dumps"
	if s, ok := newestTagged(a.s.snapshots.val, "db"); ok && src == "r2" {
		newest = "the dumps from " + stamp(s.Time) + " (" + ago(s.Time) + ")"
	}
	unit := "dashboard-restore-db-" + time.Now().Format("20060102150405") + ".service"
	argv := []string{"systemd-run", "--wait", "--quiet", "--collect", "--unit=" + unit,
		"--description=Restore the databases (dashboard)", "--property=StandardInputText=yes",
		libexec + "/restore-db", src}
	return a.typed("Replace the databases?",
		"Stops what reads them, replays "+newest+" over the live databases, and starts them again. Everything since those dumps is lost. Type restore to go ahead.",
		"restore", startCmd(spec{
			key: "restore-db", title: "Restore the databases", root: true, argv: argv, unit: unit,
			done: "Databases restored", after: []kind{kContainers},
		}))
}

func (b *backupsTab) view(a *app, w, h int) string {
	t := a.t
	var top string
	if w >= 70 {
		lw := (w - 2) / 2
		rw := w - lw - 2
		r2, drv := b.r2(a, lw-4), b.drive(a, rw-4)
		rows := max(len(r2), len(drv))
		top = lipgloss.JoinHorizontal(lipgloss.Top,
			t.box(t.bold("R2")+t.dim(" · nightly"), r2, lw, rows+2), "  ",
			t.box(t.bold("Drive")+t.dim(" · when plugged in"), drv, rw, rows+2))
	} else {
		top = t.box(t.bold("R2")+t.dim(" · nightly"), b.r2(a, w-4), w, 0) + "\n" +
			t.box(t.bold("Drive")+t.dim(" · when plugged in"), b.drive(a, w-4), w, 0)
	}
	rest := max(4, h-strings.Count(top, "\n")-3)
	return top + "\n\n" + b.snapshots(a, w, rest)
}

func (backupsTab) r2(a *app, iw int) []string {
	t := a.t
	u := a.unit("citrine-backup.service")
	timer := a.unit("citrine-backup.timer")
	kw := 10
	row := func(k, v string) string { return clip("  "+t.keyval(k, v, kw), iw) }
	var out []string
	switch {
	case u.active == "activating":
		out = append(out, t.dot(lvInfo)+" "+t.tint(lvInfo, a.spin.View()+" backing up now"))
	case u.exited.IsZero():
		out = append(out, t.dot(lvWarn)+" "+t.muted("hasn't run yet"))
	case u.result == "success":
		out = append(out, t.dot(lvOK)+" "+t.muted("backed up ")+t.text(ago(u.exited)))
	default:
		out = append(out, t.dot(lvBad)+" "+t.tint(lvBad, "failed ")+t.muted(ago(u.exited))+t.dim(" · t shows why"))
	}
	if !timer.next.IsZero() {
		out = append(out, row("next", t.accent(clock(timer.next))))
	}
	snaps := a.s.snapshots
	switch {
	case snaps.ok:
		out = append(out, row("holds", t.muted(plural(len(snaps.val), "snapshot", "snapshots"))))
		if db, ok := newestTagged(snaps.val, "db"); ok {
			out = append(out, row("database", t.muted(ago(db.Time))))
		}
		if files, ok := newestTagged(snaps.val, "files"); ok {
			out = append(out, row("files", t.muted(ago(files.Time))))
		}
	case snaps.err != nil:
		out = append(out, t.wrap("  ", t.c.dim, errText(snaps.err), iw)...)
	default:
		out = append(out, t.dim("  "+a.spin.View()+" asking R2"))
	}
	return out
}

func (backupsTab) drive(a *app, iw int) []string {
	t := a.t
	d := a.s.drive.val
	var out []string
	switch {
	case d.mounted:
		out = append(out, t.dot(lvInfo)+" "+t.tint(lvInfo, a.spin.View()+" backing up now")+t.dim(" · "+d.source))
	case d.last.IsZero():
		out = append(out, t.dot(lvWarn)+" "+t.muted("never backed up to a drive"))
	default:
		lv := lvOK
		if time.Since(d.last) > 60*24*time.Hour {
			lv = lvWarn
		}
		out = append(out, t.dot(lv)+" "+t.muted("backed up ")+t.text(ago(d.last)))
	}
	plugged := ""
	for _, dk := range a.s.disks.val {
		if dk.backup {
			plugged = dk.name
		}
	}
	switch {
	case plugged != "" && !d.mounted:
		out = append(out, "  "+t.tint(lvInfo, "plugged in as "+plugged))
	case plugged == "":
		out = append(out, t.dim("  not plugged in"))
	}
	return append(out, t.wrap("  ", t.c.dim, "Any disk labelled "+a.cfg["BACKUP_LABEL"]+" backs itself up when it's plugged in, then unmounts.", iw)...)
}

func (b *backupsTab) snapshots(a *app, w, h int) string {
	t := a.t
	r := a.s.snapshots
	if !r.ok {
		switch {
		case r.err != nil:
			return t.heading("R2 snapshots") + "\n" + t.muted(errText(r.err))
		case a.loading(kSnapshots):
			return t.heading("R2 snapshots") + "\n" + t.dim(a.spin.View()+" listing snapshots — R2 is a few hundred milliseconds away per request")
		}
		return ""
	}
	cols := []col{{title: "when", w: 17}, {title: "tags", w: 6}, {title: "id", w: 9}, {title: "added", w: 9, right: true}, {title: "files", w: 9, right: true}, {title: "size", w: 9, right: true}, {title: "", w: 0}}
	switch {
	case w < 60:
		cols = []col{{title: "when", w: 0}, {title: "tags", w: 6}, {title: "added", w: 9, right: true}}
	case w < 92:
		cols = []col{{title: "when", w: 17}, {title: "tags", w: 6}, {title: "added", w: 9, right: true}, {title: "size", w: 9, right: true}, {title: "", w: 0}}
	}
	var rows [][]string
	for i, s := range r.val {
		when := t.soft(stamp(s.Time))
		if i == b.sel {
			when = t.bold(stamp(s.Time))
		}
		tags := t.accent(strings.Join(s.Tags, ","))
		added, files, total := t.dim("—"), t.dim("—"), t.dim("—")
		if s.Summary != nil {
			added = t.muted(size(s.Summary.DataAdded))
			files = t.muted(commas(int(s.Summary.TotalFilesProcessed)))
			total = t.muted(size(s.Summary.TotalBytesProcessed))
		}
		row := []string{when, tags, t.faint(s.short()), added, files, total, t.dim(ago(s.Time))}
		switch len(cols) {
		case 3:
			row = []string{when, tags, added}
		case 5:
			row = []string{when, tags, added, total, t.dim(ago(s.Time))}
		}
		rows = append(rows, row)
	}
	b.sel = clamp(b.sel, 0, len(rows)-1)
	return strings.Join(append([]string{t.heading("R2 snapshots")}, t.table(cols, rows, b.sel, w, h-1, &b.off)...), "\n")
}
