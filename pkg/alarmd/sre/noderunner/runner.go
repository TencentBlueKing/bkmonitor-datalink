// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package noderunner

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/redact"
)

const maxOutput = 8 << 20
const maxLine = 64 << 10

// Runner owns exactly one command. All mutable state and output are serialized.
// Inputs are never persisted; the spool contains only redacted evidence.
type Runner struct {
	mu        sync.Mutex
	id, mode  string
	state     Response
	cmd       *exec.Cmd
	done      chan struct{}
	spool     *os.File
	limit     int64
	retention time.Duration
	timedOut  bool
}

func New(id, mode, directory string, retention time.Duration) (*Runner, error) {
	if id == "" || (mode != "host" && mode != "filesystem") {
		return nil, errors.New("execution ID and valid mode required")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(directory, "output.frames"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	return &Runner{id: id, mode: mode, state: Response{ExecutionID: id, State: "preparing"}, done: make(chan struct{}), spool: f, retention: retention}, nil
}

func (r *Runner) Close() error { return r.spool.Close() }
func (r *Runner) Handle(q Request) Response {
	r.mu.Lock()
	defer r.mu.Unlock()
	if q.ExecutionID != r.id {
		return r.failure("execution identity mismatch")
	}
	switch q.Action {
	case "start":
		if r.state.State != "preparing" {
			return r.snapshot(q)
		}
		if err := r.start(q); err != nil {
			r.finish("failed", err.Error(), nil)
		}
	case "cancel":
		if r.state.State == "preparing" {
			r.state.CancelRequested = true
			r.state.ConfirmedTerminated = true
			r.finish("cancelled", "", nil)
		} else if r.state.State == "running" {
			r.state.CancelRequested = true
			go r.terminate()
		}
	case "get":
	default:
		return r.failure("unknown action")
	}
	return r.snapshot(q)
}
func (r *Runner) failure(message string) Response {
	v := r.state
	v.Error = message
	v.ControlError = true
	return v
}
func (r *Runner) finish(state, message string, code *int) {
	now := time.Now().UTC()
	until := now.Add(r.retention)
	r.state.State = state
	if message != "" {
		r.state.Error = message
	}
	r.state.ExitCode = code
	r.state.FinishedAt = &now
	r.state.RetainUntil = &until
	close(r.done)
}
func (r *Runner) start(q Request) error {
	if q.Mode != r.mode || len(q.Argv) == 0 || !filepath.IsAbs(q.Cwd) || q.TimeoutSeconds < 1 || q.TimeoutSeconds > 1800 || q.OutputLimitBytes < 1 || q.OutputLimitBytes > maxOutput || len(q.Stdin) > 1<<20 {
		return errors.New("invalid execution parameters")
	}
	for key, value := range q.Env {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
			return errors.New("invalid environment")
		}
	}
	var cmd *exec.Cmd
	var err error
	if q.Mode == "host" {
		cmd, err = hostCommand(q)
	} else {
		argv := append([]string(nil), q.Argv...)
		// Filesystem diagnostics use the bundled shell even on a distroless base.
		if argv[0] == "/bin/sh" {
			if _, e := os.Stat("/data/bkmonitor/node-tools/busybox"); e == nil {
				argv = append([]string{"/data/bkmonitor/node-tools/busybox", "sh"}, argv[1:]...)
			}
		}
		path, lookupErr := resolveExecutable(argv[0], q.Cwd, environment(q.Env))
		if lookupErr != nil {
			return lookupErr
		}
		cmd = exec.Command(path, argv[1:]...)
		cmd.Dir = q.Cwd
		cmd.Stdin = strings.NewReader(q.Stdin)
		cmd.Env = environment(q.Env)
	}
	if err != nil {
		return err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	secrets := []string{}
	for k, v := range q.Env {
		k = strings.ToLower(k)
		if strings.Contains(k, "token") || strings.Contains(k, "secret") || strings.Contains(k, "password") || strings.Contains(k, "key") {
			secrets = append(secrets, v)
		}
	}
	scrub := redact.New(secrets...)
	stdout := &lineWriter{r: r, stream: 1, scrub: scrub}
	stderr := &lineWriter{r: r, stream: 2, scrub: scrub}
	// The runner holds the read ends itself rather than letting exec copy
	// them, so that the end of the output is a fact read from the pipe
	// (outputPipe.drain) and not a fixed wait after exit.
	outPipe, errPipe, err := openOutputPipes()
	if err != nil {
		for _, f := range cmd.ExtraFiles {
			_ = f.Close()
		}
		return errors.New("output pipe failed: " + err.Error())
	}
	cmd.Stdout = outPipe.write
	cmd.Stderr = errPipe.write
	r.limit = int64(q.OutputLimitBytes)
	err = cmd.Start()
	for _, f := range cmd.ExtraFiles {
		_ = f.Close()
	}
	// The child holds its own copies of the write ends; the runner's would
	// keep the pipes open for ever.
	outPipe.closeWrite()
	errPipe.closeWrite()
	if err != nil {
		outPipe.closeRead()
		errPipe.closeRead()
		return errors.New("command start failed: " + scrub.Text(err.Error()))
	}
	outDrained := outPipe.drain(stdout)
	errDrained := errPipe.drain(stderr)
	r.cmd = cmd
	r.state.State = "running"
	now := time.Now().UTC()
	r.state.StartedAt = &now
	go func() {
		timer := time.NewTimer(time.Duration(q.TimeoutSeconds) * time.Second)
		defer timer.Stop()
		select {
		case <-r.done:
			return
		case <-timer.C:
			r.mu.Lock()
			active := r.state.State == "running"
			if active {
				r.timedOut = true
			}
			r.mu.Unlock()
			if active {
				r.terminate()
			}
		}
	}()
	go func() {
		// On Linux hold the exited leader as a zombie until group signalling
		// completes. Its PID cannot be reused for an unrelated process group.
		held := waitLeaderExit(cmd.Process.Pid)
		var err, killErr error
		if held {
			killErr = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			err = cmd.Wait()
		} else {
			err = cmd.Wait()
			killErr = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		if killErr == nil || errors.Is(killErr, syscall.ESRCH) {
			reapGroup(cmd.Process.Pid)
		}
		// The group is gone: what is left in the pipes is all it wrote, and
		// is read to the end however long that takes. A pipe still open
		// once it is empty is held by a process outside the group.
		outPipe.finish()
		errPipe.finish()
		detached := <-outDrained
		detached = <-errDrained || detached
		outPipe.closeRead()
		errPipe.closeRead()
		stdout.flush()
		stderr.flush()
		r.mu.Lock()
		state := "succeeded"
		if err != nil || detached {
			state = "failed"
		}
		if r.timedOut {
			state = "timed_out"
		} else if r.state.CancelRequested {
			state = "cancelled"
		}
		code := cmd.ProcessState.ExitCode()
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			r.state.Signal = status.Signal().String()
		}
		r.state.ConfirmedTerminated = r.state.CancelRequested && (killErr == nil || errors.Is(killErr, syscall.ESRCH))
		message := ""
		if detached {
			message = "output pipe remained open after command exit; detached descendants are outside the supervision contract"
		}
		// Release argv, environment, stdin readers and redactor literals when
		// the execution ends; retained evidence needs only the final status.
		r.cmd = nil
		r.finish(state, message, &code)
		r.mu.Unlock()
	}()
	return nil
}
func environment(extra map[string]string) []string {
	env := map[string]string{"PATH": "/data/bkmonitor/node-tools:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG": "C"}
	for k, v := range extra {
		env[k] = v
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
func (r *Runner) terminate() {
	r.mu.Lock()
	if r.cmd == nil || r.state.State != "running" {
		r.mu.Unlock()
		return
	}
	pid := r.cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	r.mu.Unlock()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-r.done:
		return
	case <-timer.C:
		r.mu.Lock()
		if r.state.State == "running" {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
		r.mu.Unlock()
	}
}

// Long lines are discarded as a unit. Splitting a credential across read or
// size boundaries must never turn a partially redacted value into evidence.
type lineWriter struct {
	r         *Runner
	stream    byte
	scrub     *redact.Scrubber
	pending   []byte
	discard   bool
	discarded int64
	pem       bool
}

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for _, b := range p {
		if w.discard {
			w.discarded++
			if b == '\n' {
				w.flush()
			}
			continue
		}
		w.pending = append(w.pending, b)
		if len(w.pending) > maxLine {
			w.discard = true
			w.discarded = int64(len(w.pending))
			w.pending = nil
		}
		if b == '\n' {
			w.flush()
		}
	}
	return n, nil
}
func (w *lineWriter) flush() {
	w.r.mu.Lock()
	defer w.r.mu.Unlock()
	if w.discard {
		w.r.state.DroppedBytes += w.discarded
		w.r.state.Truncated = true
		w.discard = false
		w.discarded = 0
		return
	}
	if len(w.pending) == 0 {
		return
	}
	line := string(w.pending)
	if strings.Contains(line, "-----BEGIN ") {
		w.pem = true
	}
	masked := w.scrub.Text(line)
	if w.pem {
		masked = "[REDACTED PEM]\n"
	}
	if strings.Contains(line, "-----END ") {
		w.pem = false
	}
	data := []byte(strings.ToValidUTF8(masked, "\uFFFD"))
	w.pending = nil
	remaining := w.r.limit - w.r.state.TotalBytes
	if int64(len(data)) > remaining || len(data) > maxLine {
		w.r.state.DroppedBytes += int64(len(data))
		w.r.state.Truncated = true
		return
	}
	var header [5]byte
	header[0] = w.stream
	binary.BigEndian.PutUint32(header[1:], uint32(len(data)))
	if _, err := w.r.spool.Write(append(header[:], data...)); err != nil {
		w.r.state.Error = "output storage failed"
		w.r.state.Truncated = true
		w.r.state.DroppedBytes += int64(len(data))
		return
	}
	w.r.state.TotalBytes += int64(len(data))
}
func (r *Runner) snapshot(q Request) Response {
	v := r.state
	offset := int64(0)
	if q.OutputCursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.OutputCursor)
		prefix := r.id + ":"
		if err != nil || !strings.HasPrefix(string(raw), prefix) {
			return r.failure("invalid output cursor")
		}
		offset, err = strconv.ParseInt(strings.TrimPrefix(string(raw), prefix), 10, 64)
		if err != nil || offset < 0 || offset > v.TotalBytes {
			return r.failure("invalid output cursor")
		}
	}
	max := q.MaxBytes
	if max == 0 {
		max = 64 << 10
	}
	if max < 1 || max > 256<<10 {
		return r.failure("invalid output page size")
	}
	reader := bufio.NewReader(io.NewSectionReader(r.spool, 0, 1<<63-1))
	pos := int64(0)
	next := offset
	for next-offset < int64(max) && len(v.Output) < 1024 {
		var h [5]byte
		if _, err := io.ReadFull(reader, h[:]); err != nil {
			break
		}
		size := int(binary.BigEndian.Uint32(h[1:]))
		if size > maxLine+1024 {
			v.Error = "invalid output frame"
			break
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(reader, data); err != nil {
			v.Error = "output unavailable"
			break
		}
		end := pos + int64(size)
		if end > offset {
			start := int64(0)
			if pos < offset {
				start = offset - pos
			}
			if !utf8.RuneStart(data[start]) {
				return r.failure("output cursor splits a UTF-8 character")
			}
			take := int64(size) - start
			if take > int64(max)-(next-offset) {
				take = int64(max) - (next - offset)
			}
			for take > 0 && start+take < int64(size) && !utf8.RuneStart(data[start+take]) {
				take--
			}
			if take == 0 {
				if next != offset {
					break
				}
				_, width := utf8.DecodeRune(data[start:])
				take = int64(width)
			}
			stream := "stdout"
			if h[0] == 2 {
				stream = "stderr"
			}
			v.Output = append(v.Output, Chunk{Stream: stream, Data: string(data[start : start+take]), Offset: next})
			next += take
			if start+take < int64(size) {
				break
			}
		}
		pos = end
	}
	v.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(r.id + ":" + strconv.FormatInt(next, 10)))
	return v
}

func Serve(ctx context.Context, socket, id, mode string, retention, idle time.Duration) error {
	if err := enableSubreaper(); err != nil {
		return err
	}
	r, err := New(id, mode, filepath.Dir(socket), retention)
	if err != nil {
		return err
	}
	defer r.Close()
	// Only the dedicated supervisor process owns orphan reaping. Library
	// callers may have unrelated child processes that must not be reaped.
	go func() { <-r.done; reapAdopted() }()
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err = os.Chmod(socket, 0600); err != nil {
		return err
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		deadline := time.Now().Add(idle)
		for {
			select {
			case <-ctx.Done():
				r.Handle(Request{Action: "cancel", ExecutionID: id})
				select {
				case <-r.done:
				case <-time.After(5 * time.Second):
				}
				listener.Close()
				return
			case <-stop:
				return
			case <-ticker.C:
				r.mu.Lock()
				expired := r.state.State == "preparing" && time.Now().After(deadline) || r.state.RetainUntil != nil && time.Now().After(*r.state.RetainUntil)
				r.mu.Unlock()
				if expired {
					listener.Close()
					return
				}
			}
		}
	}()
	for {
		conn, e := listener.Accept()
		if e != nil {
			return nil
		}
		go func() {
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			var q Request
			if json.NewDecoder(io.LimitReader(conn, 16<<20)).Decode(&q) != nil {
				_ = json.NewEncoder(conn).Encode(Response{Error: "invalid control request"})
				return
			}
			_ = json.NewEncoder(conn).Encode(r.Handle(q))
		}()
	}
}
func Control(ctx context.Context, socket string, input io.Reader, output io.Writer) error {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	var q Request
	if err = json.NewDecoder(io.LimitReader(input, 16<<20)).Decode(&q); err != nil {
		return err
	}
	if err = json.NewEncoder(conn).Encode(q); err != nil {
		return err
	}
	var response Response
	if err = json.NewDecoder(io.LimitReader(conn, 4<<20)).Decode(&response); err != nil {
		return fmt.Errorf("control result unavailable: %w", err)
	}
	return json.NewEncoder(output).Encode(response)
}

// resolveExecutable searches only the requested environment and working
// directory; changing process-global PATH would race other control requests.
func resolveExecutable(name, cwd string, env []string) (string, error) {
	if strings.ContainsRune(name, '/') {
		return name, nil
	}
	path := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			path = strings.TrimPrefix(entry, "PATH=")
		}
	}
	for _, directory := range filepath.SplitList(path) {
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(cwd, directory)
		}
		candidate := filepath.Join(directory, name)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("executable not found in requested PATH: %s", name)
}
