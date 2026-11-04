// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultSymbolizerTimeout      = 120 * time.Second
	defaultMaxSymbolizedLocations = 100_000
	defaultMaxSymbolizedBytes     = 64 * 1024 * 1024
	defaultMaxELFResolvers        = 128
	maxSymbolizerDSOFileBytes     = 1024 * 1024 * 1024
	defaultMaxAddr2lineDSOs       = 4
	maxInlineFramesPerLocation    = 64
	maxSymbolizerOutputLine       = 1024 * 1024
	maxSymbolizerStringBytes      = 4096
	maxSymbolizerStderrBytes      = 64 * 1024
	symbolizerCloseGrace          = 500 * time.Millisecond
	symbolizerKillGrace           = 500 * time.Millisecond
)

var (
	errSymbolizerUnavailable = errors.New("native symbolizer is unavailable")
	errSymbolizerDSOLimit    = errors.New("addr2line DSO process limit reached")
)

type symbolizedFrame struct {
	Function  string
	File      string
	Line      int64
	StartLine int64
}

type inlineSymbolizer interface {
	Available() bool
	Backend() string
	Symbolize(dso string, address uint64) ([]symbolizedFrame, error)
	LastError() string
	Close() error
}

type symbolizerOptions struct {
	LLVMPath         string
	Addr2linePath    string
	Timeout          time.Duration
	MaxAddr2lineDSOs int
}

func discoverSymbolizerOptions() symbolizerOptions {
	options := symbolizerOptions{
		Timeout:          defaultSymbolizerTimeout,
		MaxAddr2lineDSOs: defaultMaxAddr2lineDSOs,
	}
	llvmCandidates := []string{"llvm-symbolizer"}
	// Debian/Ubuntu LLVM packages commonly expose only a version-suffixed
	// binary. JSON output is available in the supported LLVM range here.
	for version := 30; version >= 12; version-- {
		llvmCandidates = append(llvmCandidates, fmt.Sprintf("llvm-symbolizer-%d", version))
	}
	options.LLVMPath = discoverTrustedExternalCommand(llvmCandidates...)
	options.Addr2linePath = discoverTrustedExternalCommand("addr2line")
	return options
}

func discoverTrustedExternalCommand(names ...string) string {
	for _, name := range names {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if trusted := resolveTrustedExternalCommand(path); trusted != "" {
			return trusted
		}
	}
	return ""
}

// autoSymbolizer chooses one external backend for the task. LLVM is preferred;
// addr2line is only activated when LLVM is absent or its protocol fails. A
// backend failure never makes profile conversion fail.
type autoSymbolizer struct {
	ctx    context.Context
	cancel context.CancelFunc

	options symbolizerOptions
	active  inlineSymbolizer
	used    []string
	lastErr string
	closed  bool
}

func newAutoSymbolizer(parent context.Context, options symbolizerOptions) *autoSymbolizer {
	if parent == nil {
		parent = context.Background()
	}
	if options.Timeout <= 0 {
		options.Timeout = defaultSymbolizerTimeout
	}
	if options.MaxAddr2lineDSOs <= 0 {
		options.MaxAddr2lineDSOs = defaultMaxAddr2lineDSOs
	}
	ctx, cancel := context.WithTimeout(parent, options.Timeout)
	return &autoSymbolizer{ctx: ctx, cancel: cancel, options: options}
}

func (s *autoSymbolizer) Available() bool {
	return !s.closed && s.ctx.Err() == nil && (s.options.LLVMPath != "" || s.options.Addr2linePath != "")
}

func (s *autoSymbolizer) Backend() string {
	if len(s.used) > 0 {
		return strings.Join(s.used, "->")
	}
	if !s.Available() {
		return "basic"
	}
	return "not_used"
}

func (s *autoSymbolizer) LastError() string {
	return s.lastErr
}

func (s *autoSymbolizer) rememberBackend(name string) {
	if name == "" || (len(s.used) > 0 && s.used[len(s.used)-1] == name) {
		return
	}
	s.used = append(s.used, name)
}

func (s *autoSymbolizer) rememberError(err error) {
	if err == nil {
		return
	}
	s.lastErr = limitString(err.Error(), maxSymbolizerStringBytes)
}

func (s *autoSymbolizer) initPreferred() error {
	if s.closed {
		return errSymbolizerUnavailable
	}
	if s.active != nil {
		return nil
	}
	if s.options.LLVMPath != "" {
		backend, err := newLLVMSymbolizer(s.ctx, s.options.LLVMPath)
		if err == nil {
			s.active = backend
			s.rememberBackend(backend.Backend())
			return nil
		}
		s.rememberError(err)
		s.options.LLVMPath = ""
	}
	return s.initAddr2line()
}

func (s *autoSymbolizer) initAddr2line() error {
	if s.options.Addr2linePath == "" {
		return errSymbolizerUnavailable
	}
	s.active = newAddr2lineSymbolizer(s.ctx, s.options.Addr2linePath, s.options.MaxAddr2lineDSOs)
	s.rememberBackend(s.active.Backend())
	return nil
}

func (s *autoSymbolizer) Symbolize(dso string, address uint64) ([]symbolizedFrame, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.initPreferred(); err != nil {
		return nil, err
	}
	frames, err := s.active.Symbolize(dso, address)
	if err == nil {
		return normalizeSymbolizedFrames(frames), nil
	}
	s.rememberError(err)

	// Only a failed LLVM protocol switches the whole task to addr2line. An
	// addr2line failure is local to its DSO and falls back to basic symbols.
	if s.active.Backend() != "llvm" {
		return nil, err
	}
	_ = s.active.Close()
	s.active = nil
	s.options.LLVMPath = ""
	if err := s.initAddr2line(); err != nil {
		return nil, err
	}
	frames, err = s.active.Symbolize(dso, address)
	if err != nil {
		s.rememberError(err)
		return nil, err
	}
	return normalizeSymbolizedFrames(frames), nil
}

func (s *autoSymbolizer) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.active == nil {
		s.cancel()
		return nil
	}
	err := s.active.Close()
	s.cancel()
	return err
}

type llvmSymbolizer struct {
	process *symbolizerProcess
}

func newLLVMSymbolizer(ctx context.Context, path string) (*llvmSymbolizer, error) {
	process, err := startSymbolizerProcess(
		ctx,
		path,
		[]string{"--inlines", "--demangle", "--output-style=JSON"},
	)
	if err != nil {
		return nil, fmt.Errorf("start llvm-symbolizer: %w", err)
	}
	return &llvmSymbolizer{process: process}, nil
}

func (s *llvmSymbolizer) Available() bool { return s != nil && s.process != nil }

func (s *llvmSymbolizer) Backend() string { return "llvm" }

func (s *llvmSymbolizer) LastError() string { return "" }

func (s *llvmSymbolizer) Symbolize(dso string, address uint64) ([]symbolizedFrame, error) {
	if !safeLLVMSymbolizerPath(dso) {
		return nil, fmt.Errorf("llvm-symbolizer rejects unsafe DSO path")
	}
	// FILE: prevents an ordinary path beginning with BUILDID: from changing
	// LLVM's input semantics. Whitespace is rejected because the stdin
	// protocol has no portable escaping across supported LLVM versions; the
	// auto backend then safely falls back to argv-based addr2line.
	if err := s.process.writeLine(fmt.Sprintf("CODE FILE:%s 0x%x", dso, address)); err != nil {
		return nil, fmt.Errorf("write llvm-symbolizer request: %w", err)
	}
	line, err := s.process.readLine()
	if err != nil {
		return nil, fmt.Errorf("read llvm-symbolizer response: %w", err)
	}
	var response struct {
		Symbol []struct {
			FunctionName string `json:"FunctionName"`
			FileName     string `json:"FileName"`
			Line         int64  `json:"Line"`
			StartLine    int64  `json:"StartLine"`
		} `json:"Symbol"`
	}
	if err := json.Unmarshal([]byte(line), &response); err != nil {
		return nil, fmt.Errorf("parse llvm-symbolizer JSON: %w", err)
	}
	frames := make([]symbolizedFrame, 0, len(response.Symbol))
	for _, frame := range response.Symbol {
		frames = append(frames, symbolizedFrame{
			Function:  frame.FunctionName,
			File:      frame.FileName,
			Line:      frame.Line,
			StartLine: frame.StartLine,
		})
	}
	return frames, nil
}

func (s *llvmSymbolizer) Close() error {
	if s == nil || s.process == nil {
		return nil
	}
	return s.process.close()
}

type addr2lineSymbolizer struct {
	ctx       context.Context
	path      string
	maxJobs   int
	jobs      map[string]*addr2lineJob
	failedDSO map[string]error
}

func newAddr2lineSymbolizer(ctx context.Context, path string, maxJobs int) *addr2lineSymbolizer {
	return &addr2lineSymbolizer{
		ctx:       ctx,
		path:      path,
		maxJobs:   maxJobs,
		jobs:      make(map[string]*addr2lineJob),
		failedDSO: make(map[string]error),
	}
}

func (s *addr2lineSymbolizer) Available() bool { return s != nil && s.path != "" }

func (s *addr2lineSymbolizer) Backend() string { return "addr2line" }

func (s *addr2lineSymbolizer) LastError() string { return "" }

func (s *addr2lineSymbolizer) Symbolize(dso string, address uint64) ([]symbolizedFrame, error) {
	if !safeSymbolizerPath(dso) {
		return nil, fmt.Errorf("addr2line rejects unsafe DSO path")
	}
	if err, failed := s.failedDSO[dso]; failed {
		return nil, err
	}
	job := s.jobs[dso]
	if job == nil {
		if len(s.jobs) >= s.maxJobs {
			s.failedDSO[dso] = errSymbolizerDSOLimit
			return nil, errSymbolizerDSOLimit
		}
		var err error
		job, err = newAddr2lineJob(s.ctx, s.path, dso)
		if err != nil {
			s.failedDSO[dso] = err
			return nil, err
		}
		s.jobs[dso] = job
	}
	frames, err := job.symbolize(address)
	if err != nil {
		_ = job.close()
		delete(s.jobs, dso)
		s.failedDSO[dso] = err
	}
	return frames, err
}

func (s *addr2lineSymbolizer) Close() error {
	if s == nil {
		return nil
	}
	var firstErr error
	for dso, job := range s.jobs {
		if err := job.close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close addr2line for %s: %w", dso, err)
		}
	}
	s.jobs = nil
	return firstErr
}

type addr2lineJob struct {
	process *symbolizerProcess
}

func newAddr2lineJob(ctx context.Context, path, dso string) (*addr2lineJob, error) {
	// -R explicitly keeps binutils' demangler recursion limit enabled for
	// symbols originating from an untrusted workload binary.
	process, err := startSymbolizerProcess(ctx, path, []string{"-a", "-f", "-C", "-i", "-R", "-e", dso})
	if err != nil {
		return nil, fmt.Errorf("start addr2line for %s: %w", dso, err)
	}
	return &addr2lineJob{process: process}, nil
}

func (j *addr2lineJob) symbolize(address uint64) ([]symbolizedFrame, error) {
	if err := j.process.writeLine(strconv.FormatUint(address, 16)); err != nil {
		return nil, fmt.Errorf("write addr2line address: %w", err)
	}
	if err := j.process.writeLine(strconv.FormatUint(^uint64(0), 16)); err != nil {
		return nil, fmt.Errorf("write addr2line sentinel: %w", err)
	}
	header, err := j.process.readLine()
	if err != nil {
		return nil, fmt.Errorf("read addr2line address: %w", err)
	}
	if !strings.HasPrefix(header, "0x") {
		return nil, fmt.Errorf("unexpected addr2line address line %q", limitString(header, 256))
	}

	frames := make([]symbolizedFrame, 0, 4)
	for {
		functionName, err := j.process.readLine()
		if err != nil {
			return nil, fmt.Errorf("read addr2line function: %w", err)
		}
		if strings.HasPrefix(functionName, "0x") {
			// Consume the function and file lines produced for the sentinel.
			if _, err := j.process.readLine(); err != nil {
				return nil, fmt.Errorf("read addr2line sentinel function: %w", err)
			}
			if _, err := j.process.readLine(); err != nil {
				return nil, fmt.Errorf("read addr2line sentinel file: %w", err)
			}
			break
		}
		fileLine, err := j.process.readLine()
		if err != nil {
			return nil, fmt.Errorf("read addr2line file: %w", err)
		}
		file, line := parseAddr2lineFileLine(fileLine)
		if functionName == "??" {
			functionName = ""
		}
		if functionName != "" || file != "" || line != 0 {
			frames = appendBoundedSymbolizedFrame(
				frames,
				symbolizedFrame{Function: functionName, File: file, Line: line},
			)
		}
	}
	return frames, nil
}

func (j *addr2lineJob) close() error {
	if j == nil || j.process == nil {
		return nil
	}
	return j.process.close()
}

func parseAddr2lineFileLine(value string) (string, int64) {
	if value == "??:0" || value == "??:?" {
		return "", 0
	}
	if discriminator := strings.Index(value, " (discriminator"); discriminator >= 0 {
		value = value[:discriminator]
	}
	separator := strings.LastIndex(value, ":")
	if separator < 0 {
		return value, 0
	}
	line, err := strconv.ParseInt(value[separator+1:], 10, 64)
	if err != nil {
		return value, 0
	}
	return value[:separator], line
}

func normalizeSymbolizedFrames(input []symbolizedFrame) []symbolizedFrame {
	frames := make([]symbolizedFrame, 0, min(len(input), maxInlineFramesPerLocation))
	for _, frame := range input {
		frame.Function = sanitizeSymbolizerString(frame.Function)
		frame.File = sanitizeSymbolizerString(frame.File)
		if frame.Function == "??" {
			frame.Function = ""
		}
		if frame.File == "??" {
			frame.File = ""
		}
		if frame.Function == "" && frame.File == "" && frame.Line == 0 {
			continue
		}
		if len(frames) < maxInlineFramesPerLocation {
			frames = append(frames, frame)
			continue
		}
		// Preserve the outermost non-inlined frame while bounding memory.
		frames[len(frames)-1] = frame
	}
	return frames
}

func appendBoundedSymbolizedFrame(frames []symbolizedFrame, frame symbolizedFrame) []symbolizedFrame {
	if len(frames) < maxInlineFramesPerLocation {
		return append(frames, frame)
	}
	// addr2line emits leaf-to-root. Keep the leaf side and continuously
	// replace the final slot so the outermost physical frame survives.
	frames[len(frames)-1] = frame
	return frames
}

func sanitizeSymbolizerString(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > maxSymbolizerStringBytes || strings.ContainsAny(value, "\x00\r\n") {
		return ""
	}
	return value
}

func safeSymbolizerPath(value string) bool {
	return value != "" && !strings.ContainsAny(value, "\x00\r\n")
}

func safeLLVMSymbolizerPath(value string) bool {
	return safeSymbolizerPath(value) && !strings.ContainsAny(value, " \t")
}

func limitString(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}

type symbolizerProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *symbolizerStderrBuffer

	done    chan struct{}
	watch   chan struct{}
	waitErr error

	closeOnce sync.Once
	closeErr  error
}

func startSymbolizerProcess(ctx context.Context, path string, args []string) (*symbolizerProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.Command(path, args...)
	configureExternalCommand(cmd)
	cmd.Env = safeSymbolizerEnvironment()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	stderr := &symbolizerStderrBuffer{limit: maxSymbolizerStderrBytes}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
	}
	leaveResourceLimit(ctx, cmd.Process.Pid)
	preferChildOOMKill(ctx, cmd.Process.Pid)
	process := &symbolizerProcess{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReaderSize(stdout, 64*1024),
		stderr: stderr,
		done:   make(chan struct{}),
		watch:  make(chan struct{}),
	}
	go func() {
		process.waitErr = cmd.Wait()
		close(process.done)
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = interruptExternalCommand(cmd)
			timer := time.NewTimer(symbolizerCloseGrace)
			defer timer.Stop()
			select {
			case <-process.watch:
			case <-process.done:
			case <-timer.C:
				_ = killExternalCommand(cmd)
			}
		case <-process.watch:
		case <-process.done:
		}
	}()
	return process, nil
}

func (p *symbolizerProcess) writeLine(value string) error {
	select {
	case <-p.done:
		return p.processError("symbolizer exited")
	default:
	}
	_, err := io.WriteString(p.stdin, value+"\n")
	return err
}

func (p *symbolizerProcess) readLine() (string, error) {
	var output bytes.Buffer
	for {
		fragment, prefix, err := p.stdout.ReadLine()
		if len(fragment) > 0 {
			if output.Len()+len(fragment) > maxSymbolizerOutputLine {
				return "", fmt.Errorf("symbolizer output line exceeds %d bytes", maxSymbolizerOutputLine)
			}
			_, _ = output.Write(fragment)
		}
		if err != nil {
			return "", p.processError(err.Error())
		}
		if !prefix {
			return output.String(), nil
		}
	}
}

func (p *symbolizerProcess) processError(message string) error {
	select {
	case <-p.done:
		if p.waitErr != nil {
			message += ": " + p.waitErr.Error()
		}
		if stderr := strings.TrimSpace(p.stderr.String()); stderr != "" {
			message += ": " + stderr
		}
	default:
	}
	return errors.New(limitString(message, maxSymbolizerStringBytes))
}

func (p *symbolizerProcess) close() error {
	p.closeOnce.Do(func() {
		_ = p.stdin.Close()
		finished := false
		select {
		case <-p.done:
			finished = true
		case <-time.After(symbolizerCloseGrace):
			_ = killExternalCommand(p.cmd)
			select {
			case <-p.done:
				finished = true
			case <-time.After(symbolizerKillGrace):
				p.closeErr = errors.New("symbolizer did not exit after SIGKILL")
			}
		}
		if finished {
			close(p.watch)
		}
		if finished && p.waitErr != nil {
			// A process killed by the symbolization timeout is an expected
			// degradation and is already reported by Symbolize.
			p.closeErr = p.processError("close symbolizer")
		}
	})
	return p.closeErr
}

func safeSymbolizerEnvironment() []string {
	environment := []string{"LC_ALL=C", "LANG=C", "DEBUGINFOD_URLS="}
	// Active Profile only runs on Linux, but preserve the minimal variables
	// required to start a process when this package is cross-compiled/tested.
	for _, key := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP"} {
		if value := os.Getenv(key); value != "" {
			environment = append(environment, key+"="+value)
		}
	}
	return environment
}

type symbolizerStderrBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *symbolizerStderrBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	originalLength := len(value)
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return originalLength, nil
	}
	if len(value) > remaining {
		_, _ = b.buf.Write(value[:remaining])
		b.truncated = true
		return originalLength, nil
	}
	_, _ = b.buf.Write(value)
	return originalLength, nil
}

func (b *symbolizerStderrBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.truncated {
		return b.buf.String() + "\n...[truncated]"
	}
	return b.buf.String()
}
