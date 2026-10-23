// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
)

// callerCounts reads one caller family as client/caller, and /reason when
// it has one.
func callerCounts(t *testing.T, r *Recorder, family string) map[string]float64 {
	t.Helper()
	counts := map[string]float64{}
	for _, m := range gatherFamily(t, r, family) {
		labels := map[string]string{}
		for _, label := range m.GetLabel() {
			labels[label.GetName()] = label.GetValue()
		}
		key := labels["client"] + "/" + labels["caller"]
		if reason, ok := labels["reason"]; ok {
			key += "/" + reason
		}
		counts[key] = m.GetCounter().GetValue()
	}
	return counts
}

const (
	callerOperationFamily = "bkmonitor_alarmd_redis_caller_operation_total"
	callerReasonFamily    = "bkmonitor_alarmd_redis_caller_failure_reason_total"
)

// The diagnostics client's jobs each have their cells from startup, so a
// zero is a count of that job's failures and not a series never registered.
func TestTheDiagnosticsClientsJobsHaveTheirCellsFromStartup(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	operations, reasons := callerCounts(t, r, callerOperationFamily), callerCounts(t, r, callerReasonFamily)
	if len(operations) != len(redisfailure.Callers) || len(reasons) != len(redisfailure.Callers)*len(redisfailure.Reasons) {
		t.Fatalf("%d operation and %d reason cells before any call, want every job and every reason", len(operations), len(reasons))
	}
	for _, caller := range redisfailure.Callers {
		if _, ok := operations["diagnostics/"+caller]; !ok {
			t.Fatalf("no operation cell for %s: %v", caller, operations)
		}
	}
}

// An operation, and its failure, count against the job its context names -
// one call and one pipeline alike - beside the client's own counts, which
// stay as they were. A call that names no job counts against none, and a
// name outside the closed set counts as other.
func TestARedisFailureCountsAgainstTheJobThatMadeTheCall(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	hook := r.RedisHook("diagnostics")
	call := func(ctx context.Context, name string, err error) {
		ctx, _ = hook.BeforeProcess(ctx, nil)
		_ = hook.AfterProcess(ctx, failedCommand(name, err))
	}
	projection := redisfailure.WithCaller(context.Background(), redisfailure.CallerCostProjection)
	call(projection, "getrange", context.DeadlineExceeded)
	call(projection, "getrange", nil)
	call(context.Background(), "getrange", context.DeadlineExceeded)
	call(redisfailure.WithCaller(context.Background(), "cost_projecton"), "getrange", context.DeadlineExceeded)

	write := redisfailure.WithCaller(context.Background(), redisfailure.CallerDiagnosticWrite)
	ctx, _ := hook.BeforeProcessPipeline(write, nil)
	_ = hook.AfterProcessPipeline(ctx, []redis.Cmder{failedCommand("lpush", context.DeadlineExceeded), failedCommand("ltrim", context.DeadlineExceeded)})

	operations, reasons := callerCounts(t, r, callerOperationFamily), callerCounts(t, r, callerReasonFamily)
	for cell, want := range map[string]float64{
		"diagnostics/cost_projection": 2, "diagnostics/other": 1, "diagnostics/diagnostic_write": 1, "diagnostics/diagnostic_read": 0,
	} {
		if operations[cell] != want {
			t.Errorf("operations %s = %v, want %v; all %v", cell, operations[cell], want, operations)
		}
	}
	for cell, want := range map[string]float64{
		"diagnostics/cost_projection/timeout": 1, "diagnostics/other/timeout": 1, "diagnostics/diagnostic_write/timeout": 1,
		"diagnostics/directory_read/timeout": 0,
	} {
		if reasons[cell] != want {
			t.Errorf("reasons %s = %v, want %v", cell, reasons[cell], want)
		}
	}
	if got := failureReasonCounts(t, r)["diagnostics/timeout"]; got != 4 {
		t.Errorf("the client's own timeouts = %v, want all 4, named job or not", got)
	}
	for _, name := range []string{"getrange", "lrange", "lpush", "ltrim"} {
		if boundedRedisCommand(name) != name {
			t.Errorf("%s reads as %q, want its own name", name, boundedRedisCommand(name))
		}
	}
}

// hungListener accepts connections and never answers: a Sentinel that hangs.
func hungListener(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	return l.Addr().String()
}

// closedPort is an address nothing listens on: a Sentinel that refuses.
func closedPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// passedDeadline is a context whose deadline has passed and whose timer has
// not yet fired: Deadline() is behind the wall clock, Err() is still nil.
type passedDeadline struct {
	context.Context
	deadline time.Time
}

func (c passedDeadline) Deadline() (time.Time, bool) { return c.deadline, true }

// A call issued on its caller's spent deadline fails at once - every dial
// has that deadline - and a real failover client, asking each Sentinel so,
// gives up in the words of a Sentinel outage; it is named by the deadline,
// timeout, or canceled for a cancelled caller, also in the moment the
// deadline has passed and the context's timer has not fired. A call with
// time left keeps its own words: Sentinels refusing, and Sentinels hanging
// past a caller's deadline shorter than the read timeout, are an outage,
// sentinel_unreachable. A pipeline is named the same way. Each round trip is
// still timed.
func TestAFailureOnTheCallersSpentDeadlineIsNamedByTheDeadline(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	refresh := redisfailure.WithCaller(context.Background(), redisfailure.CallerDirectoryRead)
	refusing := []string{closedPort(t), closedPort(t)}
	hung := []string{hungListener(t), hungListener(t)}
	client := func(sentinels []string, readTimeout time.Duration) *redis.Client {
		c := redis.NewFailoverClient(&redis.FailoverOptions{MasterName: "mymaster", SentinelAddrs: sentinels, MaxRetries: -1,
			DialTimeout: 3 * time.Second, ReadTimeout: readTimeout})
		c.AddHook(r.RedisHook("diagnostics"))
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	passed := func() context.Context {
		return redisfailure.WithCaller(passedDeadline{Context: context.Background(), deadline: time.Now().Add(-time.Millisecond)}, redisfailure.CallerDirectoryRead)
	}
	cancelled, cancel := context.WithCancel(refresh)
	cancel()
	withTime := func(d time.Duration) context.Context {
		ctx, cancel := context.WithTimeout(refresh, d)
		t.Cleanup(cancel)
		return ctx
	}

	_ = client(refusing, 3*time.Second).GetRange(passed(), "k", 0, 10).Err()
	_ = client(hung, 3*time.Second).GetRange(passed(), "k", 0, 10).Err()
	_ = client(refusing, 3*time.Second).GetRange(cancelled, "k", 0, 10).Err()
	_ = client(refusing, 3*time.Second).GetRange(withTime(time.Second), "k", 0, 10).Err()
	_ = client(hung, 3*time.Second).GetRange(withTime(300*time.Millisecond), "k", 0, 10).Err()
	_, _ = client(refusing, 3*time.Second).Pipelined(passed(), func(pipe redis.Pipeliner) error {
		pipe.GetRange(passed(), "k", 0, 10)
		return nil
	})

	reasons := callerCounts(t, r, callerReasonFamily)
	for cell, want := range map[string]float64{
		"diagnostics/directory_read/timeout": 3, "diagnostics/directory_read/canceled": 1,
		"diagnostics/directory_read/sentinel_unreachable": 2,
	} {
		if reasons[cell] != want {
			t.Errorf("reasons %s = %v, want %v; all %v", cell, reasons[cell], want, reasons)
		}
	}
	if got := failureReasonCounts(t, r); got["diagnostics/timeout"] != 3 || got["diagnostics/sentinel_unreachable"] != 2 {
		t.Errorf("the client's own reasons = %v, want the same naming", got)
	}
	var timed uint64
	for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_redis_command_duration_seconds") {
		labels := map[string]string{}
		for _, label := range m.GetLabel() {
			labels[label.GetName()] = label.GetValue()
		}
		if labels["client"] == "diagnostics" && labels["command"] == "getrange" {
			timed += m.GetHistogram().GetSampleCount()
		}
	}
	if timed != 6 {
		t.Errorf("timed round trips = %d, want the 6 calls", timed)
	}
}

// A caller named by a hook after this one - the bundle's per-job clone of a
// shared client adds its own - is counted: the operation is counted for its
// caller once every hook has run, one call and one pipeline alike.
func TestACallerALaterHookNamesIsCounted(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	hook := r.RedisHook("source")
	ctx, _ := hook.BeforeProcess(context.Background(), nil)
	_ = hook.AfterProcess(redisfailure.WithCaller(ctx, redisfailure.CallerRuntimeState), failedCommand("mget", context.DeadlineExceeded))
	ctx, _ = hook.BeforeProcessPipeline(context.Background(), nil)
	_ = hook.AfterProcessPipeline(redisfailure.WithCaller(ctx, redisfailure.CallerStrategySource), []redis.Cmder{failedCommand("get", nil)})
	operations, reasons := callerCounts(t, r, callerOperationFamily), callerCounts(t, r, callerReasonFamily)
	if operations["source/runtime_state"] != 1 || operations["source/strategy_source"] != 1 || reasons["source/runtime_state/timeout"] != 1 {
		t.Fatalf("operations %v reasons %v, want each job's operation and the state store's timeout under the source client", operations, reasons)
	}
}
