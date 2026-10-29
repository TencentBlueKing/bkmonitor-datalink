// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.
//go:build linux

package noderunner

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

func enableSubreaper() error { return unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) }
func reapAdopted() {
	for {
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &status, 0, nil)
		if err != nil || pid <= 0 {
			return
		}
	}
}
func hostCommand(q Request) (*exec.Cmd, error) {
	if q.User == "" {
		return nil, errors.New("host user required")
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	binary, err := os.Open(executable)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(q)
	if err != nil {
		binary.Close()
		return nil, err
	}
	// BusyBox is statically linked. The already-open helper executable remains
	// available after nsenter switches the child to the host mount/root context.
	cmd := exec.Command("/data/bkmonitor/node-tools/busybox", "nsenter", "-t", "1", "-m", "-u", "-i", "-n", "-r/proc/1/root", "--", "/proc/self/fd/3", "host-child")
	cmd.ExtraFiles = []*os.File{binary}
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = environment(nil)
	return cmd, nil
}

// HostChild runs only after nsenter has entered the host namespaces/root. The
// static binary resolves host accounts and applies supplementary groups before
// irrevocably dropping privilege and execing the explicitly requested argv.
func HostChild(input io.Reader) error {
	var q Request
	if err := json.NewDecoder(io.LimitReader(input, 16<<20)).Decode(&q); err != nil {
		return err
	}
	account, err := user.Lookup(q.User)
	if err != nil {
		account, err = user.LookupId(q.User)
	}
	if err != nil {
		return errors.New("host user lookup failed")
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return err
	}
	ids, err := account.GroupIds()
	if err != nil {
		return errors.New("host supplementary groups lookup failed")
	}
	groups := make([]int, 0, len(ids))
	for _, id := range ids {
		v, e := strconv.Atoi(id)
		if e != nil {
			return e
		}
		groups = append(groups, v)
	}
	if err = syscall.Setgroups(groups); err != nil {
		return err
	}
	if err = syscall.Setgid(gid); err != nil {
		return err
	}
	if err = syscall.Setuid(uid); err != nil {
		return err
	}
	if err = os.Chdir(q.Cwd); err != nil {
		return err
	}
	if len(q.Argv) == 0 {
		return errors.New("argv required")
	}
	// Anonymous stdin survives exec without host filesystem persistence.
	_ = unix.Close(3)
	fd, err := unix.MemfdCreate("node-stdin", 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "node-stdin")
	if _, err = f.WriteString(q.Stdin); err != nil {
		return err
	}
	if _, err = f.Seek(0, 0); err != nil {
		return err
	}
	if err = unix.Dup3(fd, 0, 0); err != nil {
		return err
	}
	f.Close()
	if q.Env == nil {
		q.Env = map[string]string{}
	}
	if _, ok := q.Env["PATH"]; !ok {
		q.Env["PATH"] = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}
	env := environment(q.Env)
	path, err := resolveExecutable(q.Argv[0], q.Cwd, env)
	if err != nil {
		return err
	}

	return syscall.Exec(path, q.Argv, env)
}

func reapGroup(pgid int) {
	for {
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(-pgid, &status, 0, nil)
		if err != nil || pid <= 0 {
			return
		}
	}
}

func waitLeaderExit(pid int) bool {
	for {
		var info unix.Siginfo
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if err == unix.EINTR {
			continue
		}
		return err == nil
	}
}
