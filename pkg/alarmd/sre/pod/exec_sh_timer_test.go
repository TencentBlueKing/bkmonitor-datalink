// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pod

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

// The two halves of the stopped timer that TestStoppedTimersLeaveNoSleep
// reaches only by scheduling, pinned one at a time.

// timerSleeps lists the processes sleeping for seconds, by whatever path
// they were started: pid, ppid, pgid.
func timerSleeps(t *testing.T, seconds string) [][3]int {
	t.Helper()
	listing, err := exec.Command("ps", "-Ao", "pid=,ppid=,pgid=,args=").Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	var found [][3]int
	for _, line := range strings.Split(string(listing), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 5 || filepath.Base(fields[3]) != "sleep" || fields[4] != seconds {
			continue
		}
		var ids [3]int
		for index := range ids {
			ids[index], _ = strconv.Atoi(fields[index])
		}
		found = append(found, ids)
	}
	return found
}

// timerSeconds is a timer length past every wait of the case and this
// test binary's own, apart from the ones TestStoppedTimersLeaveNoSleep
// takes.
func timerSeconds(t *testing.T, offset int) string {
	t.Helper()
	return strconv.Itoa(int(time.Until(testwait.Deadline(t)).Seconds()) + 47 + os.Getpid()%16*4096 + 4000 + offset)
}

// A sleep can lose a TERM - one that reaches it between fork and exec does
// not end it - so a timer stopped while it waits ends its sleep with KILL.
// Where the shell gives the timer no group there is nothing after it to end
// a sleep that outlived the TERM: under dash, alone and with setsid. The
// sleep here ignores TERM, as one that lost it would, and the command lasts
// long enough that the stop comes while the timer waits.
func TestAStoppedTimerEndsASleepThatOutlivesItsTERM(t *testing.T) {
	runtimeProvider(t)
	dashOrSkip(t)
	deaf := t.TempDir()
	if err := os.WriteFile(filepath.Join(deaf, "sleep"), []byte("#!/bin/sh\ntrap '' TERM\nexec /bin/sleep \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for index, path := range []string{deaf + string(os.PathListSeparator) + os.Getenv("PATH"), deaf + string(os.PathListSeparator) + withSetsid(t)} {
		seconds := timerSeconds(t, index)
		t.Cleanup(func() {
			for _, ids := range timerSleeps(t, seconds) {
				syscall.Kill(ids[0], syscall.SIGKILL)
			}
		})
		command := exec.CommandContext(testwait.Context(t), "/bin/dash", "-c", remoteSupervisor, supervisorName, "N0NCE", seconds, "/bin/sleep", "2")
		command.Env = append(os.Environ(), "PATH="+path)
		command.WaitDelay = time.Second
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("PATH %s: supervisor: %v %s", path, err, out)
		}
		testwait.Until(t, "the timer's sleep, deaf to TERM, ending all the same", func() bool { return len(timerSleeps(t, seconds)) == 0 })
	}
}

// Under setsid the command leads a group by setsid, and the timer leads one
// by job control, turned on once setsid has launched the command: its sleep
// is in the timer's group, not the supervisor's, under a shell whose job
// control works without a tty (bash). dash and busybox ash turn it off
// without one, set -m though they accept: there the timer leads no group,
// its sleep stays in the supervisor's, and the timer ends that sleep itself
// when it is stopped (TestAStoppedTimerEndsASleepThatOutlivesItsTERM). Each
// shell this host has is asked which it is, and judged by that.
func TestUnderSetsidTheTimerLeadsAGroupOfItsOwn(t *testing.T) {
	runtimeProvider(t)
	ran := false
	for offset, shell := range hostShells(t) {
		ran = true
		groups := shellJobControl(t, shell)
		seconds := timerSeconds(t, offset)
		command := exec.CommandContext(testwait.Context(t), shell, "-c", remoteSupervisor, supervisorName, "N0NCE", seconds, "/bin/sleep", "3")
		command.Env = append(os.Environ(), "PATH="+withSetsid(t))
		command.WaitDelay = time.Second
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		supervisorGroup, err := syscall.Getpgid(command.Process.Pid)
		if err != nil {
			t.Fatalf("%s: supervisor's group: %v", shell, err)
		}
		var sleeps [][3]int
		testwait.Until(t, "the timer's sleep", func() bool { sleeps = timerSleeps(t, seconds); return len(sleeps) > 0 })
		if err := command.Wait(); err != nil {
			t.Fatalf("%s: supervisor: %v", shell, err)
		}
		if len(sleeps) != 1 || sleeps[0][1] == command.Process.Pid {
			t.Fatalf("%s: timer sleeps (pid, ppid, pgid) %v under supervisor %d: want one, the timer's child", shell, sleeps, command.Process.Pid)
		}
		if ownGroup := sleeps[0][2] == sleeps[0][1] && sleeps[0][2] != supervisorGroup; ownGroup != groups {
			t.Fatalf("%s (job control %t): timer sleeps (pid, ppid, pgid) %v under supervisor %d of group %d: in its timer's own group %t, want %t",
				shell, groups, sleeps, command.Process.Pid, supervisorGroup, ownGroup, groups)
		}
	}
	if !ran {
		t.Skip("no shell here")
	}
}

// hostShells is each of /bin/sh, /bin/bash and /bin/dash this host has, once
// each by what it resolves to.
func hostShells(t *testing.T) []string {
	t.Helper()
	var shells []string
	seen := map[string]bool{}
	for _, shell := range []string{"/bin/sh", "/bin/bash", "/bin/dash"} {
		resolved, err := filepath.EvalSymlinks(shell)
		if err != nil || seen[resolved] {
			continue
		}
		seen[resolved] = true
		shells = append(shells, shell)
	}
	return shells
}

// shellJobControl reports whether shell's job control takes effect without a
// tty, as an exec's shell runs: set -m leaves m in $- only where it does.
func shellJobControl(t *testing.T, shell string) bool {
	t.Helper()
	out, err := exec.Command(shell, "-c", `set -m 2>/dev/null; case $- in *m*) echo 1 ;; *) echo 0 ;; esac`).Output()
	if err != nil {
		t.Fatalf("%s: job control probe: %v", shell, err)
	}
	return strings.TrimSpace(string(out)) == "1"
}
