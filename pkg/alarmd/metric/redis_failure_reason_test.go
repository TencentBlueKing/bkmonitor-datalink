// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
)

// failureReasonCounts reads redis_failure_reason_total as client/reason to
// count.
func failureReasonCounts(t *testing.T, r *Recorder) map[string]float64 {
	t.Helper()
	counts := map[string]float64{}
	for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_redis_failure_reason_total") {
		labels := map[string]string{}
		for _, label := range m.GetLabel() {
			labels[label.GetName()] = label.GetValue()
		}
		counts[labels["client"]+"/"+labels["reason"]] = m.GetCounter().GetValue()
	}
	return counts
}

// replyError is an error reply from the server, as go-redis reports one.
type replyError string

func (e replyError) Error() string { return string(e) }

func (replyError) RedisError() {}

const noScript = replyError("NOSCRIPT No matching script. Please use EVAL.")

func failedCommand(name string, err error) redis.Cmder {
	cmd := redis.NewStatusCmd(context.Background(), name)
	cmd.SetErr(err)
	return cmd
}

// Every client and every reason has a cell from startup, so a zero is a
// count of failures and not a series nobody registered.
func TestRedisFailureReasonsExistFromStartupForEveryClient(t *testing.T) {
	counts := failureReasonCounts(t, NewRecorder(BuildInfo{}))
	if len(counts) != (len(redisClientNames)+1)*len(redisfailure.Reasons) {
		t.Fatalf("%d cells before any failure, want every client and reason", len(counts))
	}
	for cell, count := range counts {
		if count != 0 {
			t.Fatalf("%s = %v before any failure", cell, count)
		}
	}
}

// Every client the process wires a Redis hook to has a name of its own. A
// name outside the closed set collapses to "other", and a client counted as
// other has its load and its failures read as nobody's -- which is how the
// linkd client went unnamed.
func TestEveryWiredRedisClientHasItsOwnName(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "cmd", "alarmd", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	hook := regexp.MustCompile(`RedisHook\("([^"]*)"\)`)
	wired := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range hook.FindAllStringSubmatch(string(source), -1) {
			wired[match[1]] = true
		}
	}
	if len(wired) == 0 {
		t.Fatal("found no RedisHook call in cmd/alarmd; the scan no longer reads the wiring")
	}
	for name := range wired {
		if _, named := redisClientNames[name]; !named {
			t.Errorf("RedisHook(%q) is wired in cmd/alarmd but counts as client \"other\"", name)
		}
	}
}

// One failed operation is one count, under why it failed: a pipeline whose
// connection broke fails every member and still counts once. The empty-result
// signal and NOSCRIPT are answers, not failures.
func TestARedisOperationIsCountedOnceByWhyItFailed(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	hook := r.RedisHook("runtime")
	ctx := context.Background()
	single := func(hook *RedisCallHook, err error) {
		cmd := failedCommand("get", err)
		started, _ := hook.BeforeProcess(ctx, cmd)
		_ = hook.AfterProcess(started, cmd)
	}
	pipeline := func(errs ...error) {
		cmds := make([]redis.Cmder, len(errs))
		for index, err := range errs {
			cmds[index] = failedCommand("get", err)
		}
		started, _ := hook.BeforeProcessPipeline(ctx, cmds)
		_ = hook.AfterProcessPipeline(started, cmds)
	}
	for _, err := range []error{nil, redis.Nil, noScript, io.EOF, context.Canceled,
		replyError("WRONGTYPE Operation against a key holding the wrong kind of value")} {
		single(hook, err)
	}
	pipeline(io.EOF, io.EOF, io.EOF)
	pipeline(nil, noScript, replyError("READONLY You can't write against a read only replica."))
	pipeline(redis.Nil, noScript)
	// Mixed failures: the first failing member decides, the later one is not
	// counted as well.
	pipeline(replyError("WRONGTYPE Operation against a key holding the wrong kind of value"), io.EOF)
	single(r.RedisHook("not-a-client"), io.EOF)

	counts := failureReasonCounts(t, r)
	want := map[string]float64{
		"runtime/connection_closed": 2, "runtime/canceled": 1, "runtime/server_error": 3, "other/connection_closed": 1,
	}
	total := 0.0
	for cell, count := range counts {
		total += count
		if count != want[cell] {
			t.Errorf("%s = %v, want %v", cell, count, want[cell])
		}
	}
	if total != 7 {
		t.Fatalf("%v failures counted, want 7: %v", total, counts)
	}
}

// Through a real client: a refused dial reaches the hook as the error
// go-redis makes of it, and is named.
func TestARefusedRedisDialIsNamedThroughTheHook(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	r := NewRecorder(BuildInfo{})
	client := redis.NewClient(&redis.Options{Addr: address, MaxRetries: -1, DialTimeout: time.Second})
	defer client.Close()
	client.AddHook(r.RedisHook("source"))
	if err := client.Ping(context.Background()).Err(); err == nil {
		t.Fatal("a closed port answered")
	}
	if counts := failureReasonCounts(t, r); counts["source/connection_refused"] != 1 {
		t.Fatalf("counts %v, want the refused dial under source/connection_refused", counts)
	}
}

// Through a real client and server: an error reply is a server error, a
// NOSCRIPT is not a failure however it arrives, and a server that goes away
// is named as a connection that closed or was refused, never as other.
func TestRealRedisFailuresAreNamedThroughTheHook(t *testing.T) {
	executable := redistest.Server(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	_, port, _ := net.SplitHostPort(address)
	server := exec.Command(executable, "--port", port, "--bind", "127.0.0.1", "--save", "", "--appendonly", "no")
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			_ = server.Process.Kill()
			_ = server.Wait()
		}
	}
	defer stop()
	r := NewRecorder(BuildInfo{})
	client := redis.NewClient(&redis.Options{Addr: address, MaxRetries: -1, PoolSize: 1, DialTimeout: time.Second})
	defer client.Close()
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for client.Ping(ctx).Err() != nil {
		if time.Now().After(deadline) {
			t.Fatal("redis-server did not answer")
		}
		time.Sleep(20 * time.Millisecond)
	}
	client.AddHook(r.RedisHook("runtime"))

	if err := client.Set(ctx, "string", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HGet(ctx, "string", "field").Err(); err == nil || !strings.HasPrefix(err.Error(), "WRONGTYPE") {
		t.Fatalf("HGET on a string = %v", err)
	}
	unknown := strings.Repeat("0", 40)
	if err := client.EvalSha(ctx, unknown, nil).Err(); !noScriptReply(err) {
		t.Fatalf("EVALSHA of an unknown script = %v", err)
	}
	_, _ = client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Get(ctx, "absent")
		pipe.EvalSha(ctx, unknown, nil)
		return nil
	})
	stop()
	if err := client.Get(ctx, "string").Err(); err == nil {
		t.Fatal("a stopped server answered")
	}

	counts := failureReasonCounts(t, r)
	t.Logf("the stopped server read as connection_closed %v, connection_refused %v",
		counts["runtime/connection_closed"], counts["runtime/connection_refused"])
	gone := counts["runtime/connection_closed"] + counts["runtime/connection_refused"]
	if counts["runtime/server_error"] != 1 || gone != 1 || counts["runtime/other"] != 0 {
		t.Fatalf("counts %v, want one server error and the stopped server named as closed or refused", counts)
	}
	total := 0.0
	for _, count := range counts {
		total += count
	}
	if total != 2 {
		t.Fatalf("%v failures counted, want 2 (NOSCRIPT is not one): %v", total, counts)
	}
}
