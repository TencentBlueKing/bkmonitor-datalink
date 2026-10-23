package noderunner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

func testRunner(t *testing.T) *Runner {
	t.Helper()
	r, e := New("execution-a", "filesystem", t.TempDir(), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { r.Close() })
	return r
}
func startRequest(script string) Request {
	return Request{Action: "start", ExecutionID: "execution-a", Mode: "filesystem", Cwd: "/tmp", Argv: []string{"/bin/sh", "-s"}, Stdin: script, TimeoutSeconds: 30, OutputLimitBytes: 4096}
}
func terminal(t *testing.T, r *Runner) Response {
	t.Helper()
	select {
	case <-r.done:
	case <-testwait.Expired(t):
		t.Fatal("execution did not finish")
	}
	return r.Handle(Request{Action: "get", ExecutionID: r.id})
}
func contents(v Response) string {
	var s strings.Builder
	for _, c := range v.Output {
		s.WriteString(c.Data)
	}
	return s.String()
}
func TestOnlyOneStartAndCursor(t *testing.T) {
	r := testRunner(t)
	file := filepath.Join(t.TempDir(), "count")
	q := startRequest("echo once >> " + file + "; printf abcdef")
	r.Handle(q)
	r.Handle(q)
	v := terminal(t, r)
	if v.State != "succeeded" {
		t.Fatalf("%+v", v)
	}
	data, e := os.ReadFile(file)
	if e != nil || string(data) != "once\n" {
		t.Fatalf("executed more than once: %q %v", data, e)
	}
	p := r.Handle(Request{Action: "get", ExecutionID: r.id, MaxBytes: 3})
	if contents(p) != "abc" {
		t.Fatal(p)
	}
	p = r.Handle(Request{Action: "get", ExecutionID: r.id, OutputCursor: p.NextCursor, MaxBytes: 3})
	if contents(p) != "def" {
		t.Fatal(p)
	}
	if r.Handle(Request{Action: "get", ExecutionID: r.id, OutputCursor: "bad"}).Error == "" {
		t.Fatal("invalid cursor accepted")
	}
}
func TestCancelBeforeStart(t *testing.T) {
	r := testRunner(t)
	r.Handle(Request{Action: "cancel", ExecutionID: r.id})
	v := r.Handle(startRequest("exit 99"))
	if v.State != "cancelled" || !v.ConfirmedTerminated || v.StartedAt != nil {
		t.Fatal(v)
	}
}
func TestCancelAndDeadline(t *testing.T) {
	for _, cancel := range []bool{true, false} {
		t.Run(map[bool]string{true: "cancel", false: "deadline"}[cancel], func(t *testing.T) {
			r := testRunner(t)
			q := startRequest("sleep 30")
			q.TimeoutSeconds = 1
			r.Handle(q)
			want := "timed_out"
			if cancel {
				r.Handle(Request{Action: "cancel", ExecutionID: r.id})
				want = "cancelled"
			}
			v := terminal(t, r)
			if v.State != want {
				t.Fatal(v)
			}
		})
	}
}
func TestRedactionAcrossWritesAndBoundedLines(t *testing.T) {
	r := testRunner(t)
	q := startRequest("printf 'access_to'; sleep 0.1; printf 'ken=topsecret\\n'; printf 'Bearer split'; sleep 0.1; printf 'token\\n'; i=0; while [ $i -lt 70000 ]; do printf x; i=$((i+1)); done; printf '\\n'")
	// This tests redaction and line bounds, not shell throughput under load.
	q.TimeoutSeconds = 30
	r.Handle(q)
	select {
	case <-r.done:
	case <-testwait.Expired(t):
		t.Fatal("redaction execution did not finish")
	}
	v := terminal(t, r)
	s := contents(v)
	if strings.Contains(s, "topsecret") || strings.Contains(s, "splittoken") || !strings.Contains(s, "REDACTED") || !v.Truncated || v.DroppedBytes < 70000 {
		t.Fatalf("%+v", v)
	}
}
func TestOutputLimitDrains(t *testing.T) {
	r := testRunner(t)
	q := startRequest("i=0; while [ $i -lt 10000 ]; do echo abcdef; i=$((i+1)); done")
	q.OutputLimitBytes = 32
	r.Handle(q)
	v := terminal(t, r)
	if v.State != "succeeded" || v.TotalBytes > 32 || !v.Truncated || v.DroppedBytes == 0 {
		t.Fatal(v)
	}
}
func TestEmptyCursorCanGrow(t *testing.T) {
	r := testRunner(t)
	v := r.Handle(Request{Action: "get", ExecutionID: r.id})
	r.Handle(startRequest("printf hello"))
	terminal(t, r)
	v = r.Handle(Request{Action: "get", ExecutionID: r.id, OutputCursor: v.NextCursor})
	if contents(v) != "hello" {
		t.Fatal(v)
	}
}

func TestPEMRedaction(t *testing.T) {
	r := testRunner(t)
	r.Handle(startRequest("printf '%s\\n' '-----BEGIN PRIVATE KEY-----' sensitivebody '-----END PRIVATE KEY-----' visible"))
	v := terminal(t, r)
	if strings.Contains(contents(v), "sensitivebody") || !strings.Contains(contents(v), "visible") {
		t.Fatal(v)
	}
}

func TestCursorRejectsAnotherExecution(t *testing.T) {
	r := testRunner(t)
	other, e := New("execution-b", "filesystem", t.TempDir(), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	cursor := other.Handle(Request{Action: "get", ExecutionID: "execution-b"}).NextCursor
	if r.Handle(Request{Action: "get", ExecutionID: r.id, OutputCursor: cursor}).Error == "" {
		t.Fatal("cross execution cursor accepted")
	}
}
func TestTerminalOutputEvidenceIsStable(t *testing.T) {
	r := testRunner(t)
	r.Handle(startRequest("echo stdout; echo stderr >&2; exit 7"))
	v := terminal(t, r)
	if v.State != "failed" || v.ExitCode == nil || *v.ExitCode != 7 {
		t.Fatal(v)
	}
	first := r.Handle(Request{Action: "get", ExecutionID: r.id})
	second := r.Handle(Request{Action: "get", ExecutionID: r.id})
	if contents(first) != contents(second) || first.NextCursor != second.NextCursor {
		t.Fatal("output replay changed")
	}
}
func TestJSONEscapedStdinLimit(t *testing.T) {
	r := testRunner(t)
	q := startRequest(":" + strings.Repeat("\t", 1<<20))
	v := r.Handle(q)
	if v.State != "failed" || v.StartedAt != nil {
		t.Fatal("oversized stdin accepted")
	}
}

func TestExecutableUsesRequestedPATH(t *testing.T) {
	r := testRunner(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "custom-tool"), []byte("#!/bin/sh\necho chosen\n"), 0700); err != nil {
		t.Fatal(err)
	}
	q := startRequest("")
	q.Argv = []string{"custom-tool"}
	q.Env = map[string]string{"PATH": dir}
	r.Handle(q)
	v := terminal(t, r)
	if v.State != "succeeded" || contents(v) != "chosen\n" {
		t.Fatal(v)
	}
}

func TestUTF8PagingPreservesText(t *testing.T) {
	r := testRunner(t)
	want := "甲乙😀终点"
	r.Handle(startRequest("printf '" + want + "'"))
	terminal(t, r)
	for _, size := range []int{1, 2, 3, 4, 5, 7} {
		cursor := ""
		got := ""
		for i := 0; i < 20; i++ {
			v := r.Handle(Request{Action: "get", ExecutionID: r.id, OutputCursor: cursor, MaxBytes: size})
			if v.ControlError {
				t.Fatal(v)
			}
			got += contents(v)
			if cursor == v.NextCursor {
				break
			}
			cursor = v.NextCursor
		}
		if got != want {
			t.Fatalf("page %d = %q", size, got)
		}
	}
}
func TestBareTokenAcrossReads(t *testing.T) {
	r := testRunner(t)
	r.Handle(startRequest("printf to; sleep 0.1; printf 'ken=credential\\n'"))
	v := terminal(t, r)
	if strings.Contains(contents(v), "credential") || !strings.Contains(contents(v), "REDACTED") {
		t.Fatal(v)
	}
}
func TestControlErrorDoesNotChangeExecution(t *testing.T) {
	r := testRunner(t)
	v := r.Handle(Request{Action: "bogus", ExecutionID: r.id})
	if !v.ControlError {
		t.Fatal(v)
	}
	v = r.Handle(Request{Action: "get", ExecutionID: r.id})
	if v.ControlError || v.Error != "" || v.State != "preparing" {
		t.Fatal(v)
	}
}

func TestCredentialFieldsNeverReachSpool(t *testing.T) {
	r := testRunner(t)
	r.Handle(startRequest("printf 'to'; sleep 0.1; printf 'ken=bare-value\\n{\"token\":\"json-value\"}\\napi-key=api-value\\n'"))
	v := terminal(t, r)
	raw, err := os.ReadFile(r.spool.Name())
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"bare-value", "json-value", "api-value"} {
		if strings.Contains(contents(v), secret) || strings.Contains(string(raw), secret) {
			t.Fatalf("credential reached output or spool: %s", secret)
		}
	}
}

// The output is read to its end however long the runner takes over it. A
// command that exits with lines still in its pipe used to get two seconds
// (exec's WaitDelay) for the runner to read them; a runner slower than that
// -- here its lock held past them, as redaction and spooling under load do
// -- had the copy cut, the execution failed, and the message blamed a
// detached descendant there was none of.
func TestOutputIsReadToTheEndHoweverLongTheReaderTakes(t *testing.T) {
	r := testRunner(t)
	// The runner takes the first line and waits on its lock; the second is
	// still in the pipe when the command and its group are gone.
	r.Handle(startRequest("printf 'first\\n'; sleep 0.2; printf 'second\\n'"))
	r.mu.Lock()
	time.Sleep(2500 * time.Millisecond) // fixed-wait: holds the runner's lock past the two seconds exec's WaitDelay used to give, the slow reader the case is about
	r.mu.Unlock()
	v := terminal(t, r)
	if v.State != "succeeded" || v.Error != "" || contents(v) != "first\nsecond\n" {
		t.Fatalf("%+v", v)
	}
}

// A descendant that left the command's process group and still holds its
// output is outside what the runner supervises: once the group is gone and
// the pipe is empty, the execution ends at once and says so, rather than
// waiting on a process it does not own.
func TestADetachedDescendantHoldingTheOutputIsNamed(t *testing.T) {
	detach := ""
	if _, err := exec.LookPath("setsid"); err == nil {
		detach = "setsid sleep 3 &"
	} else if _, err := exec.LookPath("perl"); err == nil {
		detach = "perl -e 'setpgrp(0,0); sleep 3' &"
	} else {
		t.Skip("neither setsid nor perl to leave the process group with")
	}
	r := testRunner(t)
	started := time.Now()
	// The command outlives the descendant's leaving the group by a moment;
	// a group signalled before it left would take it down with the rest.
	r.Handle(startRequest("echo before; " + detach + " sleep 0.5"))
	v := terminal(t, r)
	if v.State != "failed" || !strings.Contains(v.Error, "detached descendants") || contents(v) != "before\n" {
		t.Fatalf("%+v", v)
	}
	if waited := time.Since(started); waited > 2500*time.Millisecond {
		t.Fatalf("ended after %s, want once the group was gone, not when the descendant exits", waited)
	}
}
