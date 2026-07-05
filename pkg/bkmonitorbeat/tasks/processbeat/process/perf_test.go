package process

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestGetProcState(t *testing.T) {
	mgr := &procPerfMgr{}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "single letter sleep", input: "S", want: "sleeping"},
		{name: "single letter running", input: "R", want: "running"},
		{name: "single letter blocked", input: "D", want: "idle"},
		{name: "single letter idle", input: "I", want: "idle"},
		{name: "single letter stopped", input: "T", want: "stopped"},
		{name: "single letter traced stop", input: "t", want: "stopped"},
		{name: "single letter zombie", input: "Z", want: "zombie"},
		{name: "semantic sleep", input: "sleep", want: "sleeping"},
		{name: "semantic stop", input: "stop", want: "stopped"},
		{name: "semantic zombie", input: "zombie", want: "zombie"},
		{name: "unknown", input: "x", want: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mgr.getProcState(tt.input); got != tt.want {
				t.Fatalf("getProcState(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestProcFSReaderReadsStableMetaWithSingleBootTimeLookup(t *testing.T) {
	root := t.TempDir()
	writeLinuxProc(t, root, 101, "worker 1", "S", 1, 12345, "worker\x00--config\x00x", 1000)
	writeLinuxProc(t, root, 102, "worker 2", "R", 101, 22345, "worker2\x00", 1000)

	bootReads := 0
	usernameLookups := 0
	reader := newProcFSReader(root)
	reader.bootTime = func() (uint64, error) {
		bootReads++
		return 1700000000, nil
	}
	reader.lookupUsername = func(uid string) (string, error) {
		usernameLookups++
		return "user-" + uid, nil
	}
	reader.readLink = func(name string) (string, error) {
		return "/link/" + filepath.Base(name), nil
	}

	first, err := reader.readMeta(101)
	if err != nil {
		t.Fatalf("readMeta(101) error: %v", err)
	}
	second, err := reader.readMeta(102)
	if err != nil {
		t.Fatalf("readMeta(102) error: %v", err)
	}

	if bootReads != 1 {
		t.Fatalf("boot time reads = %d, want 1", bootReads)
	}
	if usernameLookups != 1 {
		t.Fatalf("username lookups = %d, want 1", usernameLookups)
	}
	if first.Pid != 101 || first.PPid != 1 || first.Name != "worker 1" || first.Status != "sleeping" {
		t.Fatalf("unexpected first meta: %+v", first)
	}
	if first.Created != 1700000123450 {
		t.Fatalf("first created = %d, want 1700000123450", first.Created)
	}
	if first.Username != "user-1000" {
		t.Fatalf("first username = %q, want user-1000", first.Username)
	}
	if first.Cmd != "worker --config x" {
		t.Fatalf("first cmd = %q, want worker --config x", first.Cmd)
	}
	if len(first.CmdSlice) != 3 || first.CmdSlice[1] != "--config" {
		t.Fatalf("first cmd slice = %#v, want parsed argv", first.CmdSlice)
	}
	if second.PPid != 101 || second.Status != "running" || second.Created != 1700000223450 {
		t.Fatalf("unexpected second meta: %+v", second)
	}
}

func TestProcFSReaderTreatsRootAsProcFSRoot(t *testing.T) {
	procRoot := filepath.Join(t.TempDir(), "hostproc")
	writeLinuxProc(t, procRoot, 104, "host-worker", "S", 1, 42345, "host-worker\x00", 1000)

	reader := newProcFSReader(procRoot)
	reader.bootTime = func() (uint64, error) {
		return 1700000000, nil
	}
	reader.lookupUsername = func(uid string) (string, error) {
		return "user-" + uid, nil
	}

	stat, err := reader.readMeta(104)
	if err != nil {
		t.Fatalf("readMeta(104) error: %v", err)
	}
	if stat.Name != "host-worker" || stat.Pid != 104 {
		t.Fatalf("unexpected stat from proc root: %+v", stat)
	}
}

func TestProcFSReaderCompletesTruncatedProcessName(t *testing.T) {
	root := t.TempDir()
	writeLinuxProc(t, root, 103, "very-long-proce", "S", 1, 32345, "/usr/bin/very-long-process-name\x00--flag", 1000)

	reader := newProcFSReader(root)
	reader.bootTime = func() (uint64, error) {
		return 1700000000, nil
	}
	reader.lookupUsername = func(uid string) (string, error) {
		return "user-" + uid, nil
	}

	stat, err := reader.readMeta(103)
	if err != nil {
		t.Fatalf("readMeta(103) error: %v", err)
	}
	if stat.Name != "very-long-process-name" {
		t.Fatalf("name = %q, want completed long process name", stat.Name)
	}
}

func writeLinuxProc(t *testing.T, root string, pid int, name, state string, ppid int, startTicks uint64, cmdline string, uid int) {
	t.Helper()

	dir := filepath.Join(root, strconv.Itoa(pid))
	writeLinuxProcInDir(t, dir, pid, name, state, ppid, startTicks, cmdline, uid)
}

func writeLinuxProcInDir(t *testing.T, dir string, pid int, name, state string, ppid int, startTicks uint64, cmdline string, uid int) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir proc dir: %v", err)
	}
	stat := linuxStatLine(pid, name, state, ppid, startTicks)
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
		t.Fatalf("write stat: %v", err)
	}
	uidText := strconv.Itoa(uid)
	status := "Name:\t" + name + "\nState:\t" + state + "\nUid:\t" + uidText + "\t" + uidText + "\t" + uidText + "\t" + uidText + "\n"
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o644); err != nil {
		t.Fatalf("write status: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o644); err != nil {
		t.Fatalf("write cmdline: %v", err)
	}
}

func linuxStatLine(pid int, name, state string, ppid int, startTicks uint64) string {
	fields := []string{
		strconv.Itoa(pid), "(" + name + ")", state, strconv.Itoa(ppid),
		"0", "0", "0", "0", "0", "0", "0", "0",
		"0", "0", "0", "0", "0", "0", "1", "0",
		"0", strconv.FormatUint(startTicks, 10), "0", "0",
	}
	return strings.Join(fields, " ")
}
