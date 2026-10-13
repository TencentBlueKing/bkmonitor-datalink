// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

// A test binary started as "setsid" is the setsid utility: it makes a new
// session, and so a process group, and runs the rest of its arguments - the
// stand-in for the one images carry, which this machine has not.
func init() {
	if filepath.Base(os.Args[0]) != "setsid" || len(os.Args) < 2 {
		return
	}
	syscall.Setsid()
	path, err := exec.LookPath(os.Args[1])
	if err != nil {
		os.Exit(127)
	}
	syscall.Exec(path, os.Args[1:], os.Environ())
	os.Exit(126)
}

// withSetsid is a PATH with the setsid stand-in first.
func withSetsid(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, filepath.Join(dir, "setsid")); err != nil {
		t.Fatal(err)
	}
	return dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

// recordDeadline is the deadline, in seconds, of a supervised command that
// has to write its pids before the timer ends it: long enough for a shell to
// start and write them on a machine running several test suites at once,
// where one second was not.
const recordDeadline = "3"

// supervise runs remoteSupervisor under the local /bin/sh as the target's
// would: it returns the supervisor's stderr records and the command's output.
func supervise(t *testing.T, seconds string, argv ...string) (records []string, stdout, stderr string) {
	t.Helper()
	return superviseUnder(t, "/bin/sh", "", seconds, argv...)
}

// superviseUnder runs remoteSupervisor under shell, with PATH when given.
func superviseUnder(t *testing.T, shell, path, seconds string, argv ...string) (records []string, stdout, stderr string) {
	t.Helper()
	return superviseHeld(t, shell, path, seconds, false, argv...)
}

// superviseHeld is superviseUnder; with hold, the command's stdin stays open
// until the supervisor has written its group record, so a command that
// reads its stdin to the end is alive when its group is looked for. A
// command that ends before that look leaves no group to find, on a host
// that gives one or not - a race on the host's speed, not a reading of it.
func superviseHeld(t *testing.T, shell, path, seconds string, hold bool, argv ...string) (records []string, stdout, stderr string) {
	t.Helper()
	command := exec.Command(shell, append([]string{"-c", remoteSupervisor, supervisorName, "N0NCE", seconds}, argv...)...)
	if path != "" {
		command.Env = append(os.Environ(), "PATH="+path)
	}
	var out strings.Builder
	errPipe, err := command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stdin io.WriteCloser
	if hold {
		if stdin, err = command.StdinPipe(); err != nil {
			t.Fatal(err)
		}
	}
	command.Stdout = &out
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	sink := newOutputSink(1<<20, func() {})
	parser := newControlParser("N0NCE", sink, true)
	var raw []byte
	if hold {
		lines := bufio.NewReader(errPipe)
		for {
			line, err := lines.ReadBytes('\n')
			raw = append(raw, line...)
			if strings.HasPrefix(string(line), "N0NCE group ") {
				stdin.Close()
			}
			if err != nil {
				break
			}
		}
		stdin.Close()
	} else {
		raw, _ = io.ReadAll(errPipe)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("supervisor exited %v; stderr %q", err, raw)
	}
	parser.Write(raw)
	state := parser.snapshot()
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "N0NCE ") {
			records = append(records, strings.TrimPrefix(line, "N0NCE "))
		}
	}
	return records, out.String(), state.stderr
}

// hostGroups is whether this host's /bin/sh, with the PATH given (the
// test's own when empty), gives a background command a process group of its
// own: setsid on the PATH, or job control that takes effect without a tty.
// Read from the host with a probe of its own, never from the supervisor, so a
// supervisor that claims a group it did not get still fails.
func hostGroups(t *testing.T, path string) bool {
	t.Helper()
	probe := exec.Command("/bin/sh", "-c", `if command -v setsid >/dev/null 2>&1; then echo 1; exit 0; fi
set -m 2>/dev/null
sleep 5 >/dev/null 2>&1 &
p=$!
if kill -0 -"$p" 2>/dev/null; then echo 1; else echo 0; fi
kill "$p" 2>/dev/null
exit 0`)
	if path != "" {
		probe.Env = append(os.Environ(), "PATH="+path)
	}
	out, err := probe.Output()
	if err != nil {
		t.Fatalf("group probe: %v", err)
	}
	return strings.TrimSpace(string(out)) == "1"
}

// hostGroupRecord is the group record a long-lived command gets on this host.
func hostGroupRecord(t *testing.T) string {
	t.Helper()
	if hostGroups(t, "") {
		return "group 1"
	}
	return "group 0"
}

// groupingPath is a PATH under which the local /bin/sh gives a command its
// own group: the test's own where it already does, else with the setsid
// stand-in first. For the tests whose point is what a group is ended with.
func groupingPath(t *testing.T) string {
	t.Helper()
	if hostGroups(t, "") {
		return ""
	}
	return withSetsid(t)
}

// A command that ends by itself is reported with its exit code; its stdin
// reaches it, its output passes through unchanged - a last stderr line
// without a newline too - and the records are the supervisor's alone.
func TestTheShellSupervisorReportsTheCommandsEnd(t *testing.T) {
	runtimeProvider(t)
	records, stdout, stderr := superviseHeld(t, "/bin/sh", "", "5", true, "/bin/sh", "-c", "cat >/dev/null; printf 'out\\n'; printf 'last line' >&2; exit 4")
	if want := "[started " + hostGroupRecord(t) + " complete 4]"; fmt.Sprint(records) != want {
		t.Fatalf("records %q, want %s", records, want)
	}
	if stdout != "out\n" || stderr != "last line" {
		t.Fatalf("stdout %q stderr %q", stdout, stderr)
	}
}

// At its deadline the command's whole group is ended, its children's
// children too, and the records say the timer fired and the command was
// terminated.
func TestTheShellSupervisorEndsTheWholeGroupAtItsDeadline(t *testing.T) {
	runtimeProvider(t)
	pidPath := filepath.Join(t.TempDir(), "grandchild")
	started := time.Now()
	records, _, _ := superviseUnder(t, "/bin/sh", groupingPath(t), recordDeadline, "/bin/sh", "-c", "sleep 30 & echo $! > \"$1\"; sleep 30", "cmd", pidPath)
	if fmt.Sprint(records) != "[started group 1 expired complete 143]" {
		t.Fatalf("records %q, want expired then complete 143", records)
	}
	if elapsed := time.Since(started); elapsed >= 30*time.Second {
		t.Fatalf("the supervisor waited %v", elapsed)
	}
	data, err := os.ReadFile(pidPath)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid == 0 {
		t.Fatalf("the command did not record its child: %v", err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("the command's child outlived the deadline (kill -0: %v)", err)
	}
}

// A command that ignores TERM is killed after the TERM grace: the complete
// record comes no sooner than the deadline and the grace, with KILL's code.
func TestTheShellSupervisorKillsACommandThatIgnoresTERM(t *testing.T) {
	runtimeProvider(t)
	launched := time.Now()
	records, _, _ := supervise(t, "1", "/bin/sh", "-c", "trap '' TERM; while true; do sleep 1; done")
	if want := "[started " + hostGroupRecord(t) + " expired complete 137]"; fmt.Sprint(records) != want {
		t.Fatalf("records %q, want %s", records, want)
	}
	if elapsed := time.Since(launched); elapsed < 3*time.Second {
		t.Fatalf("complete after %v, sooner than the deadline and the TERM grace", elapsed)
	}
}

// Without sleep there is no timer: the supervisor says so and the command
// replaces it, so its exit code is the transport's.
func TestTheShellSupervisorWithoutSleepSaysSoAndRunsTheCommand(t *testing.T) {
	runtimeProvider(t)
	command := exec.Command("/bin/sh", "-c", remoteSupervisor, supervisorName, "N0NCE", "5", "/bin/sh", "-c", "exit 6")
	command.Env = []string{"PATH=" + t.TempDir()}
	out, err := command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 6 || !strings.Contains(string(out), "N0NCE nosleep") {
		t.Fatalf("output %q err %v, want the nosleep record and exit 6", out, err)
	}
}

// A closed output pipe does not stop the timer: the command is still ended
// at its deadline.
func TestTheShellSupervisorsDeadlineSurvivesAClosedOutput(t *testing.T) {
	runtimeProvider(t)
	pidPath := filepath.Join(t.TempDir(), "pid")
	command := exec.Command("/bin/sh", "-c", remoteSupervisor, supervisorName, "N0NCE", "1", "/bin/sh", "-c", "echo $$ > \"$1\"; while true; do sleep 1; done", "cmd", pidPath)
	errPipe, err := command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { command.Process.Kill(); command.Wait() }()
	if line, err := bufio.NewReader(errPipe).ReadString('\n'); err != nil || line != "N0NCE started\n" {
		t.Fatalf("first record %q %v", line, err)
	}
	errPipe.Close()
	waitGone(t, pidPath, time.Minute)
}

// A command whose output nobody reads blocks on a full pipe, and the timer
// ends it all the same.
func TestTheShellSupervisorsDeadlineSurvivesOutputBackpressure(t *testing.T) {
	runtimeProvider(t)
	pidPath := filepath.Join(t.TempDir(), "pid")
	command := exec.Command("/bin/sh", "-c", remoteSupervisor, supervisorName, "N0NCE", "1", "/bin/sh", "-c", "echo $$ > \"$1\"; while true; do printf 'xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx'; done", "cmd", pidPath)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdout.Close(); command.Process.Kill(); command.Wait() }()
	waitGone(t, pidPath, time.Minute)
}

// waitGone waits for the pid a command wrote to exist and then to end.
func waitGone(t *testing.T, pidPath string, guard time.Duration) {
	t.Helper()
	end := time.Now().Add(guard)
	pid := 0
	for pid == 0 && time.Now().Before(end) {
		if data, err := os.ReadFile(pidPath); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("the command never recorded its pid")
	}
	for time.Now().Before(end) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL)
	t.Fatal("the command outlived its deadline")
}

// Through the provider: a command that ends by itself completes under the
// shell's group supervision; one past its deadline is timed out with TERM's
// code; and an sh container with a missing command completes with 127 under
// supervision - not the no-shell fallback, since the shell reported started
// before the command failed.
func TestExecUnderTheShellSupervisor(t *testing.T) {
	provider, _, target := runtimeProvider(t)
	result, err := provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"/bin/sh", "-c", "cat; exit 3"}, Stdin: "in"})
	// It reads its stdin to the end and exits: it may end before its group
	// is looked for, so either supervision is right for it.
	if err != nil || result.RemoteState != RemoteCompleted || (result.Supervision != SupervisionShGroup && result.Supervision != SupervisionShPID) ||
		result.ExitCode == nil || *result.ExitCode != 3 || result.Stdout != "in" {
		t.Fatalf("completed run: %+v %v", result, err)
	}
	// A command alive at the look is supervised as this host's shell allows.
	wantSupervision := SupervisionShPID
	if hostGroups(t, "") {
		wantSupervision = SupervisionShGroup
	}
	result, err = provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"/bin/sh", "-c", "sleep 30"}, TimeoutMS: 300})
	if err != nil || result.RemoteState != RemoteTimedOut || result.Supervision != wantSupervision || result.ExitCode == nil || *result.ExitCode != 143 ||
		result.EnforcedTimeoutSeconds != 1 || result.DeclaredTimeoutMS != 300 {
		t.Fatalf("timed out run: %+v %v, want supervision %s", result, err, wantSupervision)
	}
	result, err = provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"/no/such/command"}})
	// It ends before the supervisor looks for its group, so whether it led
	// one is moot; what matters is that the shell supervised it.
	if err != nil || result.RemoteState != RemoteCompleted || (result.Supervision != SupervisionShGroup && result.Supervision != SupervisionShPID) ||
		result.ExitCode == nil || *result.ExitCode != 127 || result.UnsupervisedReason != "" {
		t.Fatalf("missing command in an sh container: %+v %v", result, err)
	}
}

// A command that ends on the timer's TERM by itself, with 0, beat the kill:
// both records arrive, and the exit code decides - completed, raced.
func TestACommandThatEndsAsTheTimerFiresIsCompletedAndRaced(t *testing.T) {
	provider, _, target := runtimeProvider(t)
	result, err := provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"/bin/sh", "-c", "trap 'exit 0' TERM; sleep 30 & wait"}, TimeoutMS: 500})
	if err != nil || result.RemoteState != RemoteCompleted || !result.DeadlineRaced || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("raced run: %+v %v", result, err)
	}
}

// recordExecutor answers as a supervisor whose records the test chooses.
func recordExecutor(lines ...string) Executor {
	return executorFunc(func(ctx context.Context, request StreamRequest) (StreamResult, error) {
		nonce := request.Argv[4]
		for _, line := range lines {
			io.WriteString(request.Stderr, strings.ReplaceAll(line, "NONCE", nonce))
		}
		zero := 0
		return StreamResult{ExitCode: &zero}, nil
	})
}

// When the timer's expired and the command's complete both arrive, the exit
// code decides: TERM's or KILL's code is the timer's, any other the
// command's own.
func TestTheExitCodeDecidesBetweenATimeoutAndARace(t *testing.T) {
	provider, _, target := runtimeProvider(t)
	for _, c := range []struct {
		exit, state string
		raced       bool
	}{{"143", RemoteTimedOut, false}, {"137", RemoteTimedOut, false}, {"0", RemoteCompleted, true}, {"2", RemoteCompleted, true}} {
		provider.options.Executor = recordExecutor("NONCE started\n", "\nNONCE group 1\n", "partial", "\nNONCE expired\n", "\nNONCE complete "+c.exit+"\n")
		result, err := provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"true"}})
		if err != nil || result.RemoteState != c.state || result.DeadlineRaced != c.raced || result.ExitCode == nil || strconv.Itoa(*result.ExitCode) != c.exit || result.Stderr != "partial" {
			t.Fatalf("exit %s: %+v %v, want %s raced=%v", c.exit, result, err, c.state, c.raced)
		}
	}
}

// noShellExecutor is a container without /bin/sh: the runtime refuses to
// start it (refused) or, as some runtimes do, the exec exits 127 with
// nothing written; anything else runs locally.
type noShellExecutor struct {
	exitInstead bool
	shellTries  int
}

func (e *noShellExecutor) Stream(ctx context.Context, request StreamRequest) (StreamResult, error) {
	if request.Argv[0] == "/bin/sh" && len(request.Argv) > 3 && request.Argv[2] == remoteSupervisor {
		e.shellTries++
		if e.exitInstead {
			code := 127
			return StreamResult{ExitCode: &code}, nil
		}
		return StreamResult{StartRefused: true}, nil
	}
	return localExecutor{}.Stream(ctx, request)
}

// A container without a shell runs the argv itself, unsupervised and said
// so, whichever way its runtime reports the missing shell; the container is
// remembered, and a replaced one is tried with the shell again. A command
// the runtime cannot start there is a start failure.
func TestAContainerWithoutAShellRunsTheCommandUnsupervised(t *testing.T) {
	for _, exitInstead := range []bool{false, true} {
		provider, client, target := runtimeProvider(t)
		executor := &noShellExecutor{exitInstead: exitInstead}
		provider.options.Executor = executor
		result, err := provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"/bin/sh", "-c", "printf direct; exit 5"}})
		if err != nil || result.RemoteState != RemoteUnsupervised || result.UnsupervisedReason != UnsupervisedNoShell || result.Supervision != SupervisionNone ||
			result.ExitCode == nil || *result.ExitCode != 5 || result.Stdout != "direct" || result.StreamCut {
			t.Fatalf("refused=%v: %+v %v", !exitInstead, result, err)
		}
		if _, err := provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"true"}}); err != nil || executor.shellTries != 1 {
			t.Fatalf("the second run tried the shell again (%d tries) or failed: %v", executor.shellTries, err)
		}
		client.pod.Status.ContainerStatuses[0].ContainerID = "containerd://replaced"
		target.ContainerID = "containerd://replaced"
		if _, err := provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"true"}}); err != nil || executor.shellTries != 2 {
			t.Fatalf("a replaced container was not tried with the shell (%d tries): %v", executor.shellTries, err)
		}
		result, err = provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"/no/such/command"}})
		requireCode(t, err, CodeCommandStartFailed)
		if result.RemoteState != RemoteNotStarted || result.StartFailure != StartCommandUnavailable {
			t.Fatalf("an unstartable command without a shell: %+v", result)
		}
	}
}

// Nothing in a shell-less container can end the command: past its deadline
// the stream is cut here, the receipt says so, and the end is unknown.
func TestAnUnsupervisedCommandPastItsDeadlineIsCut(t *testing.T) {
	provider, _, target := runtimeProvider(t)
	provider.options.Executor = &noShellExecutor{}
	started := time.Now()
	result, err := provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"/bin/sh", "-c", "sleep 30"}, TimeoutMS: 200})
	requireCode(t, err, RemoteUnknown)
	if result.RemoteState != RemoteUnsupervised || !result.StreamCut || result.ExitCode != nil {
		t.Fatalf("cut run: %+v", result)
	}
	if elapsed := time.Since(started); elapsed < StartupAllowance+200*time.Millisecond || elapsed >= 30*time.Second {
		t.Fatalf("cut after %v, want after the connection allowance and the deadline", elapsed)
	}
}

// A supervisor that fails before its first record names its exit code and
// a class of its stderr, never the text; a 126 or 127 with something
// written is such a failure, not a missing shell.
func TestASupervisorThatCannotStartSaysHow(t *testing.T) {
	provider, _, target := runtimeProvider(t)
	for _, c := range []struct {
		exit         int
		stderr, want string
	}{{2, "sh: 1: Syntax error: word unexpected\n", BootstrapSyntax}, {127, "sh: 3: printf: not found\n", BootstrapNotFound}, {1, "", BootstrapSilent}, {126, "password=visible\n", BootstrapOther}} {
		exit := c.exit
		provider.options.Executor = executorFunc(func(ctx context.Context, request StreamRequest) (StreamResult, error) {
			io.WriteString(request.Stderr, c.stderr)
			if c.stderr == "" {
				io.WriteString(request.Stdout, "x")
			}
			return StreamResult{ExitCode: &exit}, nil
		})
		result, err := provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"true"}})
		requireCode(t, err, CodeTimeoutUnsupported)
		if result.RemoteState != RemoteNotStarted || result.EvidenceScope != "wrapper_failed_before_command" || result.BootstrapExitCode == nil ||
			*result.BootstrapExitCode != c.exit || result.BootstrapClass != c.want || result.Stderr != "" || strings.Contains(fmt.Sprintf("%+v", result), "visible") {
			t.Fatalf("exit %d: %+v, want class %s", c.exit, result, c.want)
		}
	}
}

// The parser passes the command's stderr through whole: a line that ends,
// one that does not, one longer than any record, and one that looks like a
// record under another nonce; records split across writes are still read.
func TestTheControlParserKeepsTheCommandsStderrWhole(t *testing.T) {
	sink := newOutputSink(1<<20, func() {})
	parser := newControlParser("N", sink, true)
	long := strings.Repeat("y", maxControlLine+10)
	for _, chunk := range []string{"N sta", "rted\n", "\nN gro", "up 0\n", "a\n", long + "\n", "M complete 0\n", "tail", "\nN exp", "ired\n", "\nN complete 9\n"} {
		parser.Write([]byte(chunk))
	}
	state := parser.snapshot()
	if want := "a\n" + long + "\nM complete 0\ntail"; state.stderr != want || !state.started || state.group || !state.expired || !state.finished || state.exitCode != 9 {
		t.Fatalf("state %+v, stderr %q, want %q", state, state.stderr, want)
	}
}

// Past the transfer bound the stream is cut, whatever was kept.
func TestTheTransferBoundCutsTheStream(t *testing.T) {
	cut := false
	sink := newOutputSink(4, func() { cut = true })
	sink.maxTransfer = 10
	sink.write("stdout", []byte("123456"))
	if cut || !sink.truncated || sink.stdout.String() != "1234" {
		t.Fatalf("under the bound: cut=%v kept %q", cut, sink.stdout.String())
	}
	sink.write("stderr", []byte("789012"))
	if !cut {
		t.Fatal("12 bytes past a 10-byte bound did not cut the stream")
	}
}

// A command that ends while a child of its group still holds its output
// does not keep the stream open: the group goes with the command.
func TestTheShellSupervisorEndsTheGroupWhenTheCommandEnds(t *testing.T) {
	runtimeProvider(t)
	started := time.Now()
	records, _, _ := superviseUnder(t, "/bin/sh", groupingPath(t), "30", "/bin/sh", "-c", "sleep 30 & exit 0")
	if fmt.Sprint(records) != "[started group 1 complete 0]" || time.Since(started) > 10*time.Second {
		t.Fatalf("records %q after %v, want complete 0 without waiting for the child", records, time.Since(started))
	}
}

// Without sleep, through the provider: unsupervised for no_sleep, with the
// command's exit code.
func TestAShellWithoutSleepRunsTheCommandUnsupervised(t *testing.T) {
	provider, _, target := runtimeProvider(t)
	provider.options.Executor = executorFunc(func(ctx context.Context, request StreamRequest) (StreamResult, error) {
		fmt.Fprintf(request.Stderr, "%s nosleep\n", request.Argv[4])
		io.WriteString(request.Stdout, "ran")
		code := 6
		return StreamResult{ExitCode: &code}, nil
	})
	result, err := provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"true"}})
	if err != nil || result.RemoteState != RemoteUnsupervised || result.UnsupervisedReason != UnsupervisedNoSleep || result.ExitCode == nil || *result.ExitCode != 6 || result.Stdout != "ran" {
		t.Fatalf("no sleep: %+v %v", result, err)
	}
}

// A line longer than any record is the command's, even one that begins as
// a record would.
func TestALineLongerThanARecordIsTheCommands(t *testing.T) {
	parser := newControlParser("N", newOutputSink(1<<20, func() {}), true)
	long := "N complete " + strings.Repeat("9", maxControlLine)
	parser.Write([]byte("N started\n" + long + "\n"))
	if state := parser.snapshot(); state.invalid || state.finished || state.stderr != long+"\n" {
		t.Fatalf("state %+v", state)
	}
}

// The memo of containers without a shell forgets the oldest past its bound.
func TestTheShelllessMemoIsBounded(t *testing.T) {
	var memo shelllessMemo
	for index := 0; index <= maxShellless; index++ {
		memo.add(fmt.Sprintf("containerd://%d", index))
	}
	if memo.has("containerd://0") || !memo.has("containerd://1") || !memo.has(fmt.Sprintf("containerd://%d", maxShellless)) || len(memo.order) != maxShellless {
		t.Fatalf("memo holds %d", len(memo.order))
	}
}

// The runtime's refusal to start the exec's program, as containerd words
// it, is read as nothing having run; an error that does not name the
// program, or names another failure, is not.
func TestTheRuntimesRefusalToStartIsRead(t *testing.T) {
	for _, c := range []struct {
		text string
		want bool
	}{
		{`OCI runtime exec failed: exec failed: unable to start container process: exec: "/bin/sh": stat /bin/sh: no such file or directory: unknown`, true},
		{`exec: "/bin/sh": executable file not found in $PATH`, true},
		{`exec: "/bin/sh": permission denied`, true},
		{`pods "web" is forbidden: User cannot create resource "pods/exec"`, false},
		{`stat /other: no such file or directory`, false},
		{`exec: /bin/sh: no such file or directory`, false},
	} {
		if got := startRefused(errors.New(c.text), "/bin/sh"); got != c.want {
			t.Fatalf("%q read as %v", c.text, got)
		}
	}
}

// Through the Kubernetes adapter: an exec the runtime refused to start, as
// the apiserver relays containerd's refusal, is a start refused, not a
// transport failure.
func TestTheKubernetesAdapterReadsARefusedStart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"InternalError","code":500,`+
			`"message":"error executing command in container: failed to exec in container: OCI runtime exec failed: exec failed: unable to start container process: exec: \"/bin/sh\": stat /bin/sh: no such file or directory: unknown"}`)
	}))
	defer server.Close()
	executor, err := NewKubernetesExecutor(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, target := fixture()
	result, err := executor.Stream(context.Background(), StreamRequest{Target: target, Argv: []string{"/bin/sh", "-c", "script"}, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil || !result.StartRefused {
		t.Fatalf("result %+v err %v, want a refused start", result, err)
	}
}

// The supervisor's timer runs the deadline rounded up to whole seconds, and
// the receipt says both.
func TestTheDeadlineIsRoundedUpToWholeSeconds(t *testing.T) {
	provider, _, target := runtimeProvider(t)
	var seconds string
	provider.options.Executor = executorFunc(func(ctx context.Context, request StreamRequest) (StreamResult, error) {
		seconds = request.Argv[5]
		return recordExecutor("NONCE started\n", "\nNONCE group 1\n", "\nNONCE complete 0\n").Stream(ctx, request)
	})
	result, err := provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"true"}, TimeoutMS: 1500})
	if err != nil || seconds != "2" || result.EnforcedTimeoutSeconds != 2 || result.DeclaredTimeoutMS != 1500 {
		t.Fatalf("timer %q s, receipt %+v %v, want 2 s for 1500 ms", seconds, result, err)
	}
}

// A short argv[0] is matched only as the runtime quotes it, not wherever
// its letters appear in an unrelated failure.
func TestAShortProgramIsMatchedOnlyAsTheRuntimeQuotesIt(t *testing.T) {
	if startRefused(errors.New("dial tcp 192.0.2.10:10250: connect: permission denied (tls handshake for kubelet-ls)"), "ls") {
		t.Fatal("an unrelated failure naming ls was read as a refused start")
	}
	if !startRefused(errors.New(`exec: "ls": executable file not found in $PATH`), "ls") {
		t.Fatal("the runtime's refusal of ls was not read")
	}
}

// A timer stopped before its deadline leaves no sleep behind: the timer's
// own TERM ends it, whether or not the shell has job control.
func TestAStoppedTimerLeavesNoSleep(t *testing.T) {
	runtimeProvider(t)
	command := exec.Command("/bin/sh", "-c", remoteSupervisor, supervisorName, "N0NCE", "47", "/bin/sh", "-c", "exit 0")
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	time.Sleep(200 * time.Millisecond)
	listing, _ := exec.Command("ps", "-Ao", "args").Output()
	for _, line := range strings.Split(string(listing), "\n") {
		if strings.TrimSpace(line) == "sleep 47" {
			t.Fatal("the stopped timer's sleep is still running")
		}
	}
}

// dashOrSkip is the local dash, the /bin/sh of Debian-based images.
func dashOrSkip(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/bin/dash"); err != nil {
		t.Skip("no dash here")
	}
}

// dash without a tty turns job control off although set -m succeeds, as an
// exec's shell does in a Debian-based image: the supervisor finds no
// process group and says so. A command that ends by itself is still
// reported, and one past its deadline is still ended - itself, not its
// children, which the receipt's sh_pid says.
func TestUnderDashWithoutSetsidTheCommandHasNoGroupAndSaysSo(t *testing.T) {
	runtimeProvider(t)
	dashOrSkip(t)
	if records, _, _ := superviseHeld(t, "/bin/dash", "", "5", true, "/bin/sh", "-c", "cat >/dev/null; exit 4"); fmt.Sprint(records) != "[started group 0 complete 4]" {
		t.Fatalf("records %q, want no group and complete 4", records)
	}
	pidPath := filepath.Join(t.TempDir(), "pids")
	records, _, _ := superviseUnder(t, "/bin/dash", "", recordDeadline, "/bin/sh", "-c", "sleep 30 >/dev/null 2>&1 & echo $$ $! > \"$1\"; wait", "cmd", pidPath)
	data, _ := os.ReadFile(pidPath)
	var command, child int
	fmt.Sscan(string(data), &command, &child)
	t.Cleanup(func() { syscall.Kill(child, syscall.SIGKILL) })
	if fmt.Sprint(records) != "[started group 0 expired complete 143]" || command == 0 {
		t.Fatalf("records %q (pids %q), want no group, expired, complete 143", records, data)
	}
	if err := syscall.Kill(command, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("the command outlived its deadline (kill -0: %v)", err)
	}
}

// With setsid the command has its own group under any shell: under dash, and
// under a shell with job control, a command that ends by itself is reported
// in its group, and one past its deadline is ended with its children's
// children.
func TestUnderSetsidTheCommandsGroupIsEndedWhole(t *testing.T) {
	runtimeProvider(t)
	path := withSetsid(t)
	shells := []string{"/bin/sh"}
	if _, err := os.Stat("/bin/dash"); err == nil {
		shells = append(shells, "/bin/dash")
	}
	for _, shell := range shells {
		if records, _, _ := superviseHeld(t, shell, path, "5", true, "/bin/sh", "-c", "cat >/dev/null; exit 4"); fmt.Sprint(records) != "[started group 1 complete 4]" {
			t.Fatalf("%s: records %q, want a group and complete 4", shell, records)
		}
		pidPath := filepath.Join(t.TempDir(), "grandchild")
		records, _, _ := superviseUnder(t, shell, path, recordDeadline, "/bin/sh", "-c", "sleep 30 & echo $! > \"$1\"; sleep 30", "cmd", pidPath)
		if fmt.Sprint(records) != "[started group 1 expired complete 143]" {
			t.Fatalf("%s: records %q, want a group, expired, complete 143", shell, records)
		}
		data, _ := os.ReadFile(pidPath)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		if pid == 0 {
			t.Fatalf("%s: the command did not record its child", shell)
		}
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("%s: the command's child outlived the deadline (kill -0: %v)", shell, err)
		}
	}
}

// Through the provider, a dash target without setsid: the run is timed out
// and named sh_pid.
func TestExecUnderDashWithoutSetsidIsShPID(t *testing.T) {
	provider, _, target := runtimeProvider(t)
	dashOrSkip(t)
	provider.options.Executor = executorFunc(func(ctx context.Context, request StreamRequest) (StreamResult, error) {
		argv := append([]string{"/bin/dash"}, request.Argv[1:]...)
		return localExecutor{}.Stream(ctx, StreamRequest{Target: request.Target, Argv: argv, Stdin: request.Stdin, Stdout: request.Stdout, Stderr: request.Stderr})
	})
	result, err := provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"/bin/sh", "-c", "sleep 30"}, TimeoutMS: 300})
	if err != nil || result.RemoteState != RemoteTimedOut || result.Supervision != SupervisionShPID {
		t.Fatalf("dash run: %+v %v", result, err)
	}
}
