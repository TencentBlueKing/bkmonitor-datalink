// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package redistest

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

// ReadyWithin is how long a started server may take to answer.
//
// Generous on purpose. Under `go test ./...` dozens of packages start their
// own server at once, and a window that is ample on an idle machine is not on
// a loaded one: a few seconds turned a whole-tree pass into a function of
// load rather than of the code. A healthy server answers in milliseconds, so
// the budget costs the normal path nothing; it only matters when the server
// cannot start, and that is read from the server's own output.
const ReadyWithin = 30 * time.Second

// startAttempts is how many free ports Start tries. A port is found free and
// released before the server binds it, so another test can take it in
// between; the next attempt takes another.
const startAttempts = 3

// Instance is one redis-server a test started, on a local TCP address, with
// nothing saved to disk. It is stopped when the test ends.
type Instance struct {
	// Addr is the host:port it listens on.
	Addr string

	tb         testing.TB
	executable string
	dir        string
	extra      []string

	mu      sync.Mutex
	command *exec.Cmd
	exited  chan struct{}
	output  *lockedOutput
}

// Start starts a redis-server for the test on a free local port and returns
// once it answers, which is within ReadyWithin or the test fails with the
// server's output. extra is appended to the arguments every server here
// takes: no snapshot, no append-only file, its own directory, warnings only.
//
// Which redis-server, and whether a missing one skips or fails, is Server's.
//
// The readiness check asks without credentials, so a server started with
// --requirepass or an ACL never reads as ready here and the test waits out
// ReadyWithin. Start it open and set the password with CONFIG SET once it
// answers, as the startup-wait test does.
func Start(tb testing.TB, extra ...string) *Instance {
	tb.Helper()
	executable := Server(tb)
	if executable == "" {
		return nil
	}
	var reasons []string
	for attempt := 0; attempt < startAttempts; attempt++ {
		address, err := freeAddress()
		if err != nil {
			tb.Fatalf("redistest: find a free port: %v", err)
			return nil
		}
		instance := newInstance(tb, executable, address, extra)
		reason := instance.start()
		if reason == "" {
			tb.Cleanup(instance.Stop)
			return instance
		}
		reasons = append(reasons, reason)
	}
	tb.Fatalf("redistest: redis-server did not start in %d attempts: %s", startAttempts, strings.Join(reasons, "; "))
	return nil
}

// StartAt starts a redis-server on the address the test chose, for a test
// that has to know the address before the server exists.
func StartAt(tb testing.TB, address string, extra ...string) *Instance {
	tb.Helper()
	instance, reason := TryStartAt(tb, address, extra...)
	if reason != "" {
		tb.Fatalf("redistest: %s", reason)
		return nil
	}
	return instance
}

// TryStartAt is StartAt for a test that has something to do when the address
// cannot be had - another process took it meanwhile - other than fail: it
// returns why instead.
func TryStartAt(tb testing.TB, address string, extra ...string) (*Instance, string) {
	tb.Helper()
	executable := Server(tb)
	if executable == "" {
		return nil, "no redis-server"
	}
	instance := newInstance(tb, executable, address, extra)
	if reason := instance.start(); reason != "" {
		return nil, reason
	}
	tb.Cleanup(instance.Stop)
	return instance, ""
}

// FreeAddress is a local TCP address no listener holds now, for a test that
// has to name an address before anything listens on it.
func FreeAddress(tb testing.TB) string {
	tb.Helper()
	address, err := freeAddress()
	if err != nil {
		tb.Fatalf("redistest: find a free port: %v", err)
	}
	return address
}

// Client is a client for the server with the options most tests here use:
// one second to dial, read and write, and no retries, so a server that has
// gone away reads as gone rather than as slow.
func (instance *Instance) Client() *redis.Client {
	client := redis.NewClient(&redis.Options{Addr: instance.Addr, MaxRetries: -1,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	instance.tb.Cleanup(func() { _ = client.Close() })
	return client
}

// Stop kills the server and waits for it to exit. Stopping a stopped server
// does nothing.
func (instance *Instance) Stop() {
	instance.mu.Lock()
	defer instance.mu.Unlock()
	instance.stopLocked()
}

// Restart starts the server again on the same address, after stopping it if
// it is running, and returns once it answers.
func (instance *Instance) Restart() {
	instance.tb.Helper()
	instance.mu.Lock()
	instance.stopLocked()
	instance.mu.Unlock()
	if reason := instance.start(); reason != "" {
		instance.tb.Fatalf("redistest: restart: %s", reason)
	}
}

// Output is what the server wrote, for a failure message.
func (instance *Instance) Output() string {
	instance.mu.Lock()
	defer instance.mu.Unlock()
	if instance.output == nil {
		return ""
	}
	return instance.output.String()
}

func newInstance(tb testing.TB, executable, address string, extra []string) *Instance {
	return &Instance{Addr: address, tb: tb, executable: executable, dir: tb.TempDir(),
		extra: append([]string(nil), extra...)}
}

// start runs the server and waits for it, and returns why it is not serving,
// or "" once it is. It is serving when it answers on the address and reports
// its own directory back: the directory is this instance's own, so a server
// another test started on the same port is told apart from this one.
func (instance *Instance) start() string {
	host, port, err := net.SplitHostPort(instance.Addr)
	if err != nil {
		return fmt.Sprintf("address %q: %v", instance.Addr, err)
	}
	resolved, err := filepath.EvalSymlinks(instance.dir)
	if err != nil {
		return fmt.Sprintf("directory %q: %v", instance.dir, err)
	}
	arguments := append([]string{"--bind", host, "--port", port, "--save", "", "--appendonly", "no",
		"--dir", instance.dir, "--daemonize", "no", "--loglevel", "warning"}, instance.extra...)
	command := exec.Command(instance.executable, arguments...)
	output := &lockedOutput{}
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		return fmt.Sprintf("start %s: %v", instance.executable, err)
	}
	exited := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(exited)
	}()
	instance.mu.Lock()
	instance.command, instance.exited, instance.output = command, exited, output
	instance.mu.Unlock()

	probe := redis.NewClient(&redis.Options{Addr: instance.Addr, MaxRetries: -1,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	defer probe.Close()
	for deadline := time.Now().Add(ReadyWithin); time.Now().Before(deadline); {
		select {
		case <-exited:
			return fmt.Sprintf("redis-server on %s exited before it answered: %s", instance.Addr, output.String())
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := probe.Ping(ctx).Err()
		if err == nil {
			serving, dirErr := probe.ConfigGet(ctx, "dir").Result()
			cancel()
			if dirErr == nil && len(serving) == 2 && fmt.Sprint(serving[1]) == resolved {
				return ""
			}
			instance.Stop()
			return fmt.Sprintf("%s is served from %v, not this instance's %q", instance.Addr, serving, resolved)
		}
		cancel()
		time.Sleep(10 * time.Millisecond)
	}
	instance.Stop()
	return fmt.Sprintf("redis-server on %s did not answer within %s: %s", instance.Addr, ReadyWithin, output.String())
}

func (instance *Instance) stopLocked() {
	if instance.command == nil || instance.command.Process == nil {
		return
	}
	select {
	case <-instance.exited:
	default:
		_ = instance.command.Process.Kill()
		<-instance.exited
	}
	instance.command = nil
}

func freeAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	address := listener.Addr().String()
	return address, listener.Close()
}

// lockedOutput is the server's output, written by its process and read by
// the test.
type lockedOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (output *lockedOutput) Write(value []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.buffer.Write(value)
}

func (output *lockedOutput) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.buffer.String()
}
