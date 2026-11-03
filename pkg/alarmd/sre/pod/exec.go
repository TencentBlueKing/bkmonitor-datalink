// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed remote.sh
var remoteSupervisor string

// supervisorName is the supervisor's $0, what a process listing in the
// target shows for it.
const supervisorName = "alarmd-supervisor"

const (
	// maxTransferBytes bounds what one exec may stream back, kept or not:
	// output past the kept limit is read and dropped so the command is not
	// blocked on a full pipe, up to this, and past it the stream is cut.
	maxTransferBytes = 64 << 20
	// maxControlLine is the longest a supervisor record can be; a longer
	// line on stderr is the command's.
	maxControlLine = 256
	// maxBootstrapBytes bounds what the supervisor's own stderr before its
	// first record may hold; it is classified, never returned.
	maxBootstrapBytes = 4096
	// maxShellless bounds the containers remembered as having no /bin/sh.
	maxShellless = 256
)

// Exec runs one command in the target container, supervised where the
// target allows it:
//
//   - with /bin/sh, under remoteSupervisor: a POSIX shell job in its own
//     process group where the shell supports it, ended at its deadline by a
//     timer in the target, whatever happens to the connection;
//   - without /bin/sh, the argv itself: nothing in the target can end it, so
//     the receipt says remote_unsupervised (no_shell) and the stream is
//     bounded by the deadline here. The same for a shell without sleep
//     (no_sleep).
//
// Which applies is found per container, and a container found without a
// shell is remembered by its ID. A canceled connection returns unknown; the
// supervisor keeps its own timer.
func (p *Provider) Exec(ctx context.Context, request ExecRequest) (ExecReceipt, error) {
	result := ExecReceipt{Target: request.Target, StartedAt: p.options.Now().UTC(), RemoteState: RemoteNotStarted, EvidenceScope: "not_executed", RedactionContract: RedactionContract}
	result.RequestID = request.RequestID
	if result.RequestID == "" {
		id, err := randomID()
		if err != nil {
			return result, err
		}
		result.RequestID = id
	}
	finish := func() { result.FinishedAt = p.options.Now().UTC() }
	release, err := p.acquire(ctx)
	if err != nil {
		finish()
		return result, err
	}
	defer release()
	if p.options.Executor == nil {
		finish()
		return result, &Error{Code: CodeTimeoutUnsupported, Message: "Kubernetes exec adapter is unavailable"}
	}
	if len(request.Argv) == 0 || len(request.Argv) > 128 || request.Argv[0] == "" || int64(len(request.Stdin)) > p.options.MaxStdinBytes {
		finish()
		return result, invalid("argv and stdin exceed the execution contract")
	}
	argvBytes := 0
	for _, arg := range request.Argv {
		argvBytes += len(arg)
		if strings.ContainsRune(arg, '\x00') {
			finish()
			return result, invalid("argv contains a NUL byte")
		}
	}
	if argvBytes > 32<<10 {
		finish()
		return result, invalid("argv byte budget exceeded")
	}
	limit, err := p.outputLimit(request.OutputBytes)
	if err != nil {
		finish()
		return result, err
	}
	timeout := p.options.DefaultTimeout
	if request.TimeoutMS != 0 {
		if request.TimeoutMS < 1 || request.TimeoutMS > p.options.MaxTimeout.Milliseconds() {
			finish()
			return result, invalid("execution deadline exceeds provider limit")
		}
		timeout = time.Duration(request.TimeoutMS) * time.Millisecond
	}
	result.DeclaredTimeoutMS = timeout.Milliseconds()
	encoded, _ := json.Marshal(struct {
		Argv  []string `json:"argv"`
		Stdin string   `json:"stdin"`
	}{request.Argv, request.Stdin})
	digest := sha256.Sum256(encoded)
	result.ScriptDigest = "sha256:" + hex.EncodeToString(digest[:])
	verifyCtx, verifyCancel := context.WithTimeout(ctx, VerificationTimeout)
	observation, err := p.resolve(verifyCtx, request.Target, true)
	verifyCancel()
	if err != nil {
		var failure *Error
		if errors.As(err, &failure) && failure.Code == CodeTargetChanged {
			result.TargetChanged = true
			result.ObservedAfter = &observation
			result.EvidenceScope = "identity_changed_before_execution"
		}
		finish()
		return result, err
	}
	if observation.Phase != "Running" || !observation.Running || observation.Target.ContainerID == "" || observation.Target.ImageID == "" {
		finish()
		return result, &Error{Code: CodeTargetNotRunning, Message: "target container has no running identity"}
	}
	result.EvidenceScope = "identity_verified_before"
	if p.shellless.has(observation.Target.ContainerID) {
		return p.execDirect(ctx, request, limit, timeout, result, finish)
	}
	nonce, err := randomID()
	if err != nil {
		finish()
		return result, err
	}
	// POSIX sleep takes whole seconds: the deadline is rounded up, and the
	// receipt carries both.
	seconds := int64(math.Ceil(timeout.Seconds()))
	remoteArgv := append([]string{"/bin/sh", "-c", remoteSupervisor, supervisorName, nonce, strconv.FormatInt(seconds, 10)}, request.Argv...)
	allowance := p.options.startupAllowance
	if allowance <= 0 {
		allowance = StartupAllowance
	}
	run := p.runStream(ctx, request, remoteArgv, nonce, limit, allowance, time.Duration(seconds)*time.Second+TerminationGrace, false)
	if run.shellMissing() {
		p.shellless.add(observation.Target.ContainerID)
		return p.execDirect(ctx, request, limit, timeout, result, finish)
	}
	state := run.state
	p.fillOutput(&result, state, limit)
	switch {
	case state.nosleep:
		result.Supervision, result.UnsupervisedReason = SupervisionNone, UnsupervisedNoSleep
		return p.finishUnsupervised(ctx, request, run, result, finish)
	case !state.started && !run.cut.Load() && run.err == nil && run.transport.ExitCode != nil && *run.transport.ExitCode != 0:
		// The shell ran and failed before its first record, which it writes
		// before launching the command (and exits if it cannot): the
		// command never started. An exit 0 without the record is no
		// supervisor's and is unknown.
		result.RemoteState = RemoteNotStarted
		result.EvidenceScope = "wrapper_failed_before_command"
		code := *run.transport.ExitCode
		result.BootstrapExitCode = &code
		result.BootstrapClass = bootstrapClass(state.bootstrap)
		finish()
		return result, &Error{Code: CodeTimeoutUnsupported, Message: "the supervisor could not start on the target"}
	}
	result.EnforcedTimeoutSeconds = seconds
	// What the supervisor found after the launch; a run cut before it said
	// leaves the supervision unnamed.
	if state.groupKnown {
		result.Supervision = SupervisionShPID
		if state.group {
			result.Supervision = SupervisionShGroup
		}
	}
	switch {
	case run.err != nil || state.invalid || !state.finished || run.transport.ExitCode == nil || *run.transport.ExitCode != 0:
		result.RemoteState = RemoteUnknown
	default:
		exit := state.exitCode
		result.ExitCode = &exit
		result.RemoteState = RemoteCompleted
		if state.expired {
			// The timer fired; the exit code says whether it ended the
			// command or the command ended first (per the design's race
			// rule: 128 + TERM or KILL is the timer's).
			if exit == 128+15 || exit == 128+9 {
				result.RemoteState = RemoteTimedOut
			} else {
				result.DeadlineRaced = true
			}
		}
	}
	result.ObservedAfter, result.TargetChanged, result.EvidenceScope = p.postObserve(ctx, request.Target)
	finish()
	if result.TargetChanged {
		return result, &Error{Code: CodeTargetChanged, Message: "target identity changed during execution"}
	}
	if result.RemoteState == RemoteUnknown {
		return result, &Error{Code: RemoteUnknown, Message: "remote termination is unconfirmed; inspect existing execution before explicit retry"}
	}
	return result, nil
}

// execDirect runs the argv itself in a container with no shell: nothing in
// the target can end it, so the stream is bounded here by the deadline
// (after StartupAllowance for the connection) and the receipt says so.
func (p *Provider) execDirect(ctx context.Context, request ExecRequest, limit int64, timeout time.Duration, result ExecReceipt, finish func()) (ExecReceipt, error) {
	result.Supervision, result.UnsupervisedReason = SupervisionNone, UnsupervisedNoShell
	run := p.runStream(ctx, request, request.Argv, "", limit, 0, StartupAllowance+timeout, true)
	if run.transport.StartRefused {
		result.RemoteState = RemoteNotStarted
		result.EvidenceScope = "command_not_started"
		result.StartFailure = StartCommandUnavailable
		finish()
		return result, &Error{Code: CodeCommandStartFailed, Message: "the target could not start the command"}
	}
	p.fillOutput(&result, run.state, limit)
	return p.finishUnsupervised(ctx, request, run, result, finish)
}

// finishUnsupervised reads a run nothing in the target bounded: its exit
// code when it ended inside the deadline, the cut when it did not.
func (p *Provider) finishUnsupervised(ctx context.Context, request ExecRequest, run streamRun, result ExecReceipt, finish func()) (ExecReceipt, error) {
	result.RemoteState = RemoteUnsupervised
	result.StreamCut = run.cut.Load()
	if !result.StreamCut && run.err == nil && run.transport.ExitCode != nil {
		exit := *run.transport.ExitCode
		result.ExitCode = &exit
	}
	result.ObservedAfter, result.TargetChanged, result.EvidenceScope = p.postObserve(ctx, request.Target)
	finish()
	if result.TargetChanged {
		return result, &Error{Code: CodeTargetChanged, Message: "target identity changed during execution"}
	}
	if result.ExitCode == nil {
		return result, &Error{Code: RemoteUnknown, Message: "unsupervised command did not end inside its deadline; it may still run in the target"}
	}
	return result, nil
}

func (p *Provider) fillOutput(result *ExecReceipt, state protocolState, limit int64) {
	var stdoutCut, stderrCut bool
	result.Stdout, stdoutCut = p.redactor.bounded(state.stdout, limit)
	remaining := limit - int64(len(result.Stdout))
	result.Stderr, stderrCut = p.redactor.bounded(state.stderr, remaining)
	result.Truncated = state.truncated || stdoutCut || stderrCut
}

// streamRun is one exec stream: what its records and output said, how the
// transport ended, and whether this side cut it.
type streamRun struct {
	state     protocolState
	transport StreamResult
	err       error
	cut       *atomic.Bool
}

// shellMissing is a supervised launch that found no /bin/sh: the runtime
// refused to start it, or (on runtimes that report it so) it exited 126 or
// 127 with nothing written at all. Either way nothing ran, so running the
// argv directly instead is not a second run.
func (run streamRun) shellMissing() bool {
	if run.cut.Load() || run.state.started || run.state.nosleep {
		return false
	}
	if run.transport.StartRefused {
		return true
	}
	return run.err == nil && run.transport.ExitCode != nil && (*run.transport.ExitCode == 126 || *run.transport.ExitCode == 127) &&
		run.state.written == 0
}

// runStream runs one exec. A supervised stream is bounded by allowance until
// its first record and by bound after it; a direct one (started) by bound
// from the call.
func (p *Provider) runStream(ctx context.Context, request ExecRequest, argv []string, nonce string, limit int64, allowance, bound time.Duration, direct bool) streamRun {
	execCtx, execCancel := context.WithCancel(ctx)
	defer execCancel()
	run := streamRun{cut: &atomic.Bool{}}
	sink := newOutputSink(limit, func() { run.cut.Store(true); execCancel() })
	control := newControlParser(nonce, sink, !direct)
	if direct {
		close(control.started)
		allowance = bound
	}
	streamDone := make(chan struct{})
	go bindStream(execCancel, run.cut, control.started, streamDone, allowance, bound)
	run.transport, run.err = p.options.Executor.Stream(execCtx, StreamRequest{Target: request.Target, Argv: argv, Stdin: strings.NewReader(request.Stdin),
		Stdout: sink.writer("stdout"), Stderr: control})
	close(streamDone)
	run.state = control.snapshot()
	return run
}

// bindStream bounds one exec stream. Until the supervisor's first record it
// has allowance: connection setup and a shell's start on a loaded node do
// not consume the command's time, and a stream cut there answers unknown,
// since the target may still start the command, which then runs to its own
// timeout. After it, bound: the deadline and TerminationGrace for TERM, KILL
// and the terminal record. The command deadline itself is enforced inside
// the target, independently of this timer.
func bindStream(cancel context.CancelFunc, cut *atomic.Bool, started, done <-chan struct{}, allowance, bound time.Duration) {
	startup := time.NewTimer(allowance)
	defer startup.Stop()
	select {
	case <-started:
	case <-startup.C:
		cut.Store(true)
		cancel()
		return
	case <-done:
		return
	}
	remote := time.NewTimer(bound)
	defer remote.Stop()
	select {
	case <-remote.C:
		cut.Store(true)
		cancel()
	case <-done:
	}
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", &Error{Code: "request_id_unavailable", Message: "secure request identity is unavailable"}
	}
	return hex.EncodeToString(value[:]), nil
}

// bootstrapClass names what the supervisor's own stderr before its first
// record says, by category only: the text can carry anything and is never
// returned.
func bootstrapClass(stderr string) string {
	line := strings.ToLower(strings.SplitN(stderr, "\n", 2)[0])
	switch {
	case line == "":
		return BootstrapSilent
	case strings.Contains(line, "not found") || strings.Contains(line, "no such file"):
		return BootstrapNotFound
	case strings.Contains(line, "permission denied") || strings.Contains(line, "not permitted"):
		return BootstrapPermission
	case strings.Contains(line, "syntax"):
		return BootstrapSyntax
	default:
		return BootstrapOther
	}
}

// outputSink keeps the command's output up to the limit, both streams
// counted together, drops the rest while counting it, and cuts the stream
// once maxTransferBytes have passed.
type outputSink struct {
	mu             sync.Mutex
	limit, kept    int64
	maxTransfer    int64
	transferred    int64
	stdoutBytes    int64
	truncated      bool
	stdout, stderr bytes.Buffer
	cut            func()
}

func newOutputSink(limit int64, cut func()) *outputSink {
	return &outputSink{limit: limit, maxTransfer: maxTransferBytes, cut: cut}
}

func (s *outputSink) write(stream string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transferred += int64(len(data))
	if stream == "stdout" {
		s.stdoutBytes += int64(len(data))
	}
	if s.transferred > s.maxTransfer {
		s.truncated = true
		s.cut()
		return
	}
	if remaining := s.limit - s.kept; int64(len(data)) > remaining {
		data = data[:max(0, remaining)]
		s.truncated = true
	}
	s.kept += int64(len(data))
	if stream == "stdout" {
		s.stdout.Write(data)
	} else {
		s.stderr.Write(data)
	}
}

type sinkWriter struct {
	sink   *outputSink
	stream string
}

func (w sinkWriter) Write(data []byte) (int, error) {
	w.sink.write(w.stream, data)
	return len(data), nil
}

func (s *outputSink) writer(stream string) sinkWriter { return sinkWriter{sink: s, stream: stream} }

// controlParser reads the exec's stderr: the command's stderr, and in a
// supervised run the supervisor's records, each a whole line starting with
// the nonce. The newline the supervisor writes before expired and complete
// - so they start a line after a command line that did not end - is its
// own and is dropped. Before the first record, stderr is the supervisor's
// and is kept aside, bounded, for its class only.
type controlParser struct {
	mu         sync.Mutex
	nonce      string
	supervised bool
	sink       *outputSink
	line       []byte
	// long is a line already past maxControlLine, the command's however it
	// ends; held is a newline not yet passed on, because the next line may
	// be a record it belongs to.
	long, held bool
	state      protocolState
	bootstrap  bytes.Buffer
	started    chan struct{}
	startOnce  sync.Once
}

// protocolState is what a stream's records and output said.
type protocolState struct {
	stdout, stderr                   string
	bootstrap                        string
	started, nosleep, group, expired bool
	groupKnown                       bool
	finished, invalid, truncated     bool
	exitCode                         int
	written                          int64
}

func newControlParser(nonce string, sink *outputSink, supervised bool) *controlParser {
	return &controlParser{nonce: nonce, sink: sink, supervised: supervised, started: make(chan struct{})}
}

func (c *controlParser) Write(data []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	size := len(data)
	c.state.written += int64(size)
	if !c.supervised {
		c.sink.write("stderr", data)
		return size, nil
	}
	for len(data) > 0 {
		newline := bytes.IndexByte(data, '\n')
		if newline < 0 {
			c.take(data)
			break
		}
		c.take(data[:newline])
		c.endLine()
		data = data[newline+1:]
	}
	return size, nil
}

// take adds bytes to the current line; a line past maxControlLine cannot be
// a record and is passed on as it comes.
func (c *controlParser) take(data []byte) {
	if c.long {
		c.content(data)
		return
	}
	c.line = append(c.line, data...)
	if len(c.line) > maxControlLine {
		c.flushHeld()
		c.content(c.line)
		c.line, c.long = c.line[:0], true
	}
}

func (c *controlParser) endLine() {
	if c.long {
		c.long = false
		c.held = true
		return
	}
	line := string(c.line)
	c.line = c.line[:0]
	if c.record(line) {
		return
	}
	c.flushHeld()
	c.content([]byte(line))
	c.held = true
}

// record applies one line if it is a record of this run's nonce.
func (c *controlParser) record(line string) bool {
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != c.nonce || c.state.finished {
		return false
	}
	switch {
	case fields[1] == "started" && len(fields) == 2 && !c.state.started && !c.state.nosleep:
		c.state.started = true
		c.held = false
		c.startOnce.Do(func() { close(c.started) })
	case fields[1] == "nosleep" && len(fields) == 2 && !c.state.started && !c.state.nosleep:
		c.state.nosleep = true
		c.held = false
		c.startOnce.Do(func() { close(c.started) })
	case fields[1] == "group" && len(fields) == 3 && c.state.started && !c.state.groupKnown:
		c.state.groupKnown, c.state.group = true, fields[2] == "1"
		c.held = false
	case fields[1] == "expired" && len(fields) == 2 && c.state.started:
		c.state.expired = true
		c.held = false
	case fields[1] == "complete" && len(fields) == 3 && c.state.started:
		code, err := strconv.Atoi(fields[2])
		if err != nil {
			c.state.invalid = true
			return true
		}
		c.state.finished, c.state.exitCode = true, code
		c.held = false
	default:
		return false
	}
	return true
}

// content passes command stderr on; before the first record it is the
// supervisor's own, kept aside for its class.
func (c *controlParser) content(data []byte) {
	if !c.state.started && !c.state.nosleep {
		if room := maxBootstrapBytes - c.bootstrap.Len(); room > 0 {
			c.bootstrap.Write(data[:min(len(data), room)])
		}
		return
	}
	c.sink.write("stderr", data)
}

func (c *controlParser) flushHeld() {
	if c.held {
		c.held = false
		c.content([]byte{'\n'})
	}
}

func (c *controlParser) snapshot() protocolState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.line) > 0 && !c.long {
		// A last line without a newline is the command's.
		c.flushHeld()
		c.content(c.line)
		c.line = c.line[:0]
	} else {
		c.flushHeld()
	}
	state := c.state
	state.bootstrap = c.bootstrap.String()
	c.sink.mu.Lock()
	state.stdout, state.stderr = c.sink.stdout.String(), c.sink.stderr.String()
	state.truncated = c.sink.truncated
	state.written += c.sink.stdoutBytes
	c.sink.mu.Unlock()
	return state
}

// shelllessMemo is the containers found without /bin/sh, by container ID,
// the oldest forgotten first past maxShellless.
type shelllessMemo struct {
	mu    sync.Mutex
	order []string
	set   map[string]struct{}
}

func (m *shelllessMemo) has(containerID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.set[containerID]
	return ok
}

func (m *shelllessMemo) add(containerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.set == nil {
		m.set = make(map[string]struct{})
	}
	if _, ok := m.set[containerID]; ok {
		return
	}
	if len(m.order) >= maxShellless {
		delete(m.set, m.order[0])
		m.order = m.order[1:]
	}
	m.order = append(m.order, containerID)
	m.set[containerID] = struct{}{}
}
