// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRedisCallHookCountsEveryPipelinedMember(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	hook := recorder.RedisHook("runtime")
	if hook == nil {
		t.Fatal("recorder must expose a Redis hook")
	}
	moment := time.Unix(0, 0)
	hook.now = func() time.Time { return moment }

	ctx, err := hook.BeforeProcessPipeline(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeforeProcessPipeline: %v", err)
	}
	moment = moment.Add(3 * time.Millisecond)
	batch := []redis.Cmder{
		redis.NewStringCmd(ctx, "get", "a"),
		redis.NewStringCmd(ctx, "get", "b"),
		redis.NewStatusCmd(ctx, "set", "c", "1"),
	}
	if err := hook.AfterProcessPipeline(ctx, batch); err != nil {
		t.Fatalf("AfterProcessPipeline: %v", err)
	}

	// The shared Redis master is billed per command, so a batch of three must
	// count as three, not as one round trip.
	if got := testutil.ToFloat64(hook.metrics.calls.WithLabelValues("runtime", "get", "true")); got != 2 {
		t.Fatalf("pipelined get count = %v, want 2", got)
	}
	if got := testutil.ToFloat64(hook.metrics.calls.WithLabelValues("runtime", "set", "true")); got != 1 {
		t.Fatalf("pipelined set count = %v, want 1", got)
	}
}

func TestRedisCallHookBoundsCommandNamesAndRecordsFailures(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	hook := recorder.RedisHook("runtime")
	moment := time.Unix(0, 0)
	hook.now = func() time.Time { return moment }

	ctx, err := hook.BeforeProcess(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeforeProcess: %v", err)
	}
	moment = moment.Add(time.Millisecond)
	unknown := redis.NewStringCmd(ctx, "bitfield_ro", "k")
	unknown.SetErr(redis.Nil)
	if err := hook.AfterProcess(ctx, unknown); err != nil {
		t.Fatalf("AfterProcess: %v", err)
	}
	if got := testutil.ToFloat64(hook.metrics.calls.WithLabelValues("runtime", "other", "false")); got != 1 {
		t.Fatalf("unbounded command must collapse to other, got %v", got)
	}
	// redis.Nil is the empty-result signal, not a failure.
	if got := testutil.ToFloat64(hook.metrics.failures.WithLabelValues("runtime", "other", "false")); got != 0 {
		t.Fatalf("redis.Nil must not count as a failure, got %v", got)
	}

	ctx, _ = hook.BeforeProcess(context.Background(), nil)
	failed := redis.NewStringCmd(ctx, "GET", "k")
	failed.SetErr(context.DeadlineExceeded)
	if err := hook.AfterProcess(ctx, failed); err != nil {
		t.Fatalf("AfterProcess: %v", err)
	}
	if got := testutil.ToFloat64(hook.metrics.failures.WithLabelValues("runtime", "get", "false")); got != 1 {
		t.Fatalf("real error must count as a failure, got %v", got)
	}
}

// Retries leave no trace of their own: the hook times the whole retry loop and
// only the final outcome reaches the failure counter. Counting operations makes
// them derivable, because the pool counts one acquisition per attempt.
func TestRedisOperationCounterCountsCallsNotCommands(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	hook := recorder.RedisHook("runtime")
	ctx := context.Background()
	single, err := hook.BeforeProcess(ctx, redis.NewStringCmd(ctx, "get", "k"))
	if err != nil {
		t.Fatal(err)
	}
	if err := hook.AfterProcess(single, redis.NewStringCmd(ctx, "get", "k")); err != nil {
		t.Fatal(err)
	}
	batch := []redis.Cmder{redis.NewStatusCmd(ctx, "set", "a", "1"), redis.NewStatusCmd(ctx, "set", "b", "2")}
	batched, err := hook.BeforeProcessPipeline(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if err := hook.AfterProcessPipeline(batched, batch); err != nil {
		t.Fatal(err)
	}
	operations := testutil.ToFloat64(hook.metrics.operations)
	if operations != 2 {
		t.Fatalf("operations = %v, want 2 (one call plus one batch, not three commands)", operations)
	}
}

// "Redis 命令率涨了" 在这个进程里是不可行动的信息：它同时开着控制面、运行时和
// 兼容输出几个客户端。没有这一维，诊断突发和控制面负载在指标上长得一模一样，
// 而把这两个客户端分开正是当初拆连接池的全部理由。
func TestRedisCallHookAttributesLoadToItsClient(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	source := recorder.RedisHook("source")
	runtime := recorder.RedisHook("runtime")

	for _, hook := range []*RedisCallHook{source, source, runtime} {
		ctx, err := hook.BeforeProcess(context.Background(), redis.NewStringCmd(context.Background(), "get", "k"))
		if err != nil {
			t.Fatal(err)
		}
		if err := hook.AfterProcess(ctx, redis.NewStringCmd(context.Background(), "get", "k")); err != nil {
			t.Fatal(err)
		}
	}

	if got := testutil.ToFloat64(source.metrics.calls.WithLabelValues("source", "get", "false")); got != 2 {
		t.Fatalf("source calls = %v, want 2", got)
	}
	if got := testutil.ToFloat64(source.metrics.calls.WithLabelValues("runtime", "get", "false")); got != 1 {
		t.Fatalf("runtime calls = %v, want 1", got)
	}
	if got := testutil.ToFloat64(source.metrics.operations.WithLabelValues("source")); got != 2 {
		t.Fatalf("source operations = %v, want 2", got)
	}
}

// 客户端名字写在接线代码里，写错一个就会凭空多一条 series，而且没人会发现。
func TestRedisCallHookClosesTheClientLabel(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	hook := recorder.RedisHook("a-client-that-was-never-declared")
	if hook.client != "other" {
		t.Fatalf("client = %q, want the closed bucket", hook.client)
	}
}

// The per-client health beside the counters: when a command last completed
// and last failed, what the failure said, per connection and shared by every
// hook the recorder hands out for that name. The empty-result signal is a
// completed command, not a failure; a failed member of a pipeline fails the
// batch; and a name outside the closed list reads under "other", where the
// counters already put it.
func TestRedisCallHookRecordsPerClientHealth(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	if _, known := recorder.RedisClientHealth("source"); known {
		t.Fatal("a client no hook has reported on reads as known")
	}
	hook := recorder.RedisHook("source")
	moment := time.Unix(1000, 0)
	hook.now = func() time.Time { return moment }
	ctx, _ := hook.BeforeProcess(context.Background(), nil)
	ok := redis.NewStringCmd(ctx, "get", "a")
	if err := hook.AfterProcess(ctx, ok); err != nil {
		t.Fatal(err)
	}
	health, known := recorder.RedisClientHealth("source")
	if !known || !health.LastSuccessAt.Equal(moment) || !health.LastFailureAt.IsZero() || health.LastFailure != "" {
		t.Fatalf("after a success: %+v known %v", health, known)
	}
	// The empty-result signal completes the command.
	moment = moment.Add(time.Second)
	missing := redis.NewStringCmd(ctx, "get", "b")
	missing.SetErr(redis.Nil)
	_ = hook.AfterProcess(ctx, missing)
	if health, _ = recorder.RedisClientHealth("source"); !health.LastSuccessAt.Equal(moment) || !health.LastFailureAt.IsZero() {
		t.Fatalf("redis.Nil was recorded as a failure: %+v", health)
	}
	// A real failure, with a credential-shaped address in its text.
	moment = moment.Add(time.Second)
	failed := redis.NewStringCmd(ctx, "get", "c")
	failed.SetErr(errors.New("dial redis://user:secret@redis.example:6379: connection refused"))
	_ = hook.AfterProcess(ctx, failed)
	health, _ = recorder.RedisClientHealth("source")
	if !health.LastFailureAt.Equal(moment) || !health.LastSuccessAt.Equal(moment.Add(-time.Second)) {
		t.Fatalf("after a failure: %+v", health)
	}
	if health.LastFailure != "dial redis://***@redis.example:6379: connection refused" {
		t.Errorf("failure text = %q, want the credentials replaced", health.LastFailure)
	}
	// A second hook for the same name writes the same record: a role served
	// off a reused connection reads that connection's health.
	other := recorder.RedisHook("source")
	other.now = func() time.Time { return moment.Add(5 * time.Second) }
	octx, _ := other.BeforeProcessPipeline(context.Background(), nil)
	batch := []redis.Cmder{redis.NewStringCmd(octx, "get", "a"), redis.NewStringCmd(octx, "get", "b")}
	batch[1].SetErr(errors.New("READONLY You can't write against a read only replica"))
	_ = other.AfterProcessPipeline(octx, batch)
	health, _ = recorder.RedisClientHealth("source")
	if !health.LastFailureAt.Equal(moment.Add(5*time.Second)) || health.LastFailure != "READONLY You can't write against a read only replica" {
		t.Errorf("a failed pipeline member did not fail the batch on the shared record: %+v", health)
	}
	// A clean pipeline completes.
	octx, _ = other.BeforeProcessPipeline(context.Background(), nil)
	other.now = func() time.Time { return moment.Add(6 * time.Second) }
	_ = other.AfterProcessPipeline(octx, []redis.Cmder{redis.NewStringCmd(octx, "get", "a")})
	if health, _ = recorder.RedisClientHealth("source"); !health.LastSuccessAt.Equal(moment.Add(6 * time.Second)) {
		t.Errorf("a clean pipeline did not complete: %+v", health)
	}
	// Names outside the list fold to other, on the record as on the counters.
	unknown := recorder.RedisHook("nobody")
	uctx, _ := unknown.BeforeProcess(context.Background(), nil)
	_ = unknown.AfterProcess(uctx, redis.NewStringCmd(uctx, "get", "a"))
	if _, known := recorder.RedisClientHealth("other"); !known {
		t.Error("an unlisted client did not record under other")
	}
	if _, known := recorder.RedisClientHealth("dynamic_config"); known {
		t.Error("dynamic_config reads as known before any hook reported on it")
	}
	// The text is bounded.
	long := redis.NewStringCmd(ctx, "get", "d")
	long.SetErr(errors.New(strings.Repeat("x", 300)))
	_ = hook.AfterProcess(ctx, long)
	if health, _ = recorder.RedisClientHealth("source"); len(health.LastFailure) != redisClientHealthTextLimit+3 {
		t.Errorf("failure text length = %d, want %d plus the ellipsis", len(health.LastFailure), redisClientHealthTextLimit)
	}
	var none *Recorder
	if _, known := none.RedisClientHealth("source"); known {
		t.Error("a nil recorder reads as known")
	}
}

// NOSCRIPT is the server saying it has not got the script and the client
// sending it: not a failure of anything, and it sat in LastFailure on a live
// dependency table for as long as nothing else failed. It has its own count
// and clock, the failure record does not move, and the reply is matched on
// the server's word alone -- an error that merely mentions the word elsewhere
// is a failure like any other.
func TestRedisCallHookKeepsScriptCacheMissesApartFromFailures(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	hook := recorder.RedisHook("source")
	moment := time.Unix(1000, 0)
	hook.now = func() time.Time { return moment }
	ctx, _ := hook.BeforeProcess(context.Background(), nil)
	miss := redis.NewCmd(ctx, "evalsha", "deadbeef", 1, "k")
	miss.SetErr(errors.New("NOSCRIPT No matching script. Please use EVAL."))
	_ = hook.AfterProcess(ctx, miss)
	health, _ := recorder.RedisClientHealth("source")
	if health.ScriptCacheMisses != 1 || !health.LastScriptCacheMissAt.Equal(moment) {
		t.Fatalf("after a NOSCRIPT reply: misses %d at %s, want 1 at %s", health.ScriptCacheMisses, health.LastScriptCacheMissAt, moment)
	}
	if !health.LastFailureAt.IsZero() || health.LastFailure != "" || !health.LastSuccessAt.IsZero() {
		t.Fatalf("a NOSCRIPT reply moved the failure or success record: %+v", health)
	}
	// A second miss, later, in a pipeline: the count and the clock move.
	moment = moment.Add(7 * time.Second)
	pctx, _ := hook.BeforeProcessPipeline(context.Background(), nil)
	batch := []redis.Cmder{redis.NewStringCmd(pctx, "get", "a"), redis.NewCmd(pctx, "evalsha", "deadbeef", 1, "k")}
	batch[1].SetErr(errors.New("NOSCRIPT No matching script. Please use EVAL."))
	_ = hook.AfterProcessPipeline(pctx, batch)
	health, _ = recorder.RedisClientHealth("source")
	if health.ScriptCacheMisses != 2 || !health.LastScriptCacheMissAt.Equal(moment) || !health.LastFailureAt.IsZero() {
		t.Fatalf("after a second miss in a pipeline: %+v, want 2 misses at %s and no failure", health, moment)
	}
	// A failure whose text mentions the word elsewhere is a failure.
	moment = moment.Add(time.Second)
	other := redis.NewCmd(ctx, "evalsha", "deadbeef", 1, "k")
	other.SetErr(errors.New("ERR Error running script: NOSCRIPT is not what this is"))
	_ = hook.AfterProcess(ctx, other)
	health, _ = recorder.RedisClientHealth("source")
	if health.ScriptCacheMisses != 2 || !health.LastFailureAt.Equal(moment) || health.LastFailure == "" {
		t.Fatalf("an error mentioning the word was read as a cache miss: %+v", health)
	}
	// The failure counter still counts the error reply: it counts replies,
	// and the dependency table is where the two are told apart.
	if got := testutil.ToFloat64(recorder.phaseTwo.redisCalls.failures.WithLabelValues("source", "evalsha", "false")); got != 2 {
		t.Errorf("evalsha failures = %v, want 2: the miss and the error, both error replies", got)
	}
}
