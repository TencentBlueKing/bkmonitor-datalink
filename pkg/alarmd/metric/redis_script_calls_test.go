// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisscript"
)

const (
	testScriptBody = "return 'metric script calls test'"
	testOtherBody  = "return 'metric script calls test, not registered'"
	testScriptName = "metric_script_calls_test"
	testSecondBody = "return 'metric script calls test, second'"
	testSecondName = "metric_script_calls_second_test"
)

var (
	testScriptSHA = redis.NewScript(testScriptBody).Hash()
	_             = redisscript.Register(testScriptName, testScriptBody)
	_             = redisscript.Register(testSecondName, testSecondBody)
)

func evalShaCommand(ctx context.Context, sha string, err error, reply interface{}) *redis.Cmd {
	cmd := redis.NewCmd(ctx, "evalsha", sha, 1, "key", "argument")
	if err != nil {
		cmd.SetErr(err)
		return cmd
	}
	cmd.SetVal(reply)
	return cmd
}

func scriptLoadCommand(ctx context.Context, body string) *redis.StringCmd {
	cmd := redis.NewStringCmd(ctx, "script", "load", body)
	cmd.SetVal(strings.Repeat("a", 40))
	return cmd
}

func runOne(t *testing.T, hook *RedisCallHook, ctx context.Context, cmd redis.Cmder) {
	t.Helper()
	issued, err := hook.BeforeProcess(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if err := hook.AfterProcess(issued, cmd); err != nil {
		t.Fatalf("the hook returned %v: it must never fail a command", err)
	}
}

func runBatch(t *testing.T, hook *RedisCallHook, ctx context.Context, cmds []redis.Cmder) {
	t.Helper()
	issued, err := hook.BeforeProcessPipeline(ctx, cmds)
	if err != nil {
		t.Fatal(err)
	}
	if err := hook.AfterProcessPipeline(issued, cmds); err != nil {
		t.Fatalf("the hook returned %v: it must never fail a batch", err)
	}
}

func TestAScriptCommandReadsUnderItsNameAndOutcome(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		name    string
		err     error
		outcome string
	}{
		{"a reply", nil, redisScriptOutcomeOK},
		{"an empty reply", redis.Nil, redisScriptOutcomeOK},
		{"a cold cache", replyError("NOSCRIPT No matching script"), redisScriptOutcomeNoScript},
		{"an error reply", replyError("ERR boom"), redisScriptOutcomeError},
		{"no reply", errors.New("dial tcp: connection refused"), redisScriptOutcomeError},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := NewRecorder(BuildInfo{})
			hook := recorder.RedisHook("runtime")
			runOne(t, hook, ctx, evalShaCommand(ctx, testScriptSHA, test.err, int64(1)))
			calls := recorder.phaseTwo.redisCalls.scriptCalls
			if got := testutil.ToFloat64(calls.WithLabelValues(testScriptName, "evalsha", "false", test.outcome)); got != 1 {
				t.Fatalf("redis_script_total for outcome %s = %v", test.outcome, got)
			}
			if got := testutil.CollectAndCount(calls); got != 1 {
				t.Fatalf("%d series, want only the one", got)
			}
		})
	}
}

func TestEvalReadsByItsTextAndAnUnregisteredScriptAsOther(t *testing.T) {
	ctx := context.Background()
	recorder := NewRecorder(BuildInfo{})
	hook := recorder.RedisHook("runtime")
	eval := redis.NewCmd(ctx, "eval", testScriptBody, 0)
	eval.SetVal(int64(1))
	runOne(t, hook, ctx, eval)
	unknown := redis.NewCmd(ctx, "eval", testOtherBody, 0)
	unknown.SetVal(int64(1))
	runOne(t, hook, ctx, unknown)
	runOne(t, hook, ctx, evalShaCommand(ctx, redis.NewScript(testOtherBody).Hash(), nil, int64(1)))

	metrics := recorder.phaseTwo.redisCalls
	if got := testutil.ToFloat64(metrics.scriptCalls.WithLabelValues(testScriptName, "eval", "false", "ok")); got != 1 {
		t.Errorf("eval by text = %v", got)
	}
	if got := testutil.ToFloat64(metrics.scriptCalls.WithLabelValues(redisscript.Other, "eval", "false", "ok")); got != 1 {
		t.Errorf("unregistered eval = %v", got)
	}
	if got := testutil.ToFloat64(metrics.scriptCalls.WithLabelValues(redisscript.Other, "evalsha", "false", "ok")); got != 1 {
		t.Errorf("unregistered evalsha = %v", got)
	}
	// Neither a text nor a SHA-1 may become a label.
	for _, label := range redisLabelValues(t, recorder, "alarmd_redis_script_total", "script") {
		if label != testScriptName && label != redisscript.Other {
			t.Errorf("script label %q", label)
		}
	}
}

func TestACommandThatIsNotAScriptReadsOnlyInTheCommandFamilies(t *testing.T) {
	ctx := context.Background()
	recorder := NewRecorder(BuildInfo{})
	hook := recorder.RedisHook("runtime")
	get := redis.NewStringCmd(ctx, "get", testScriptBody)
	get.SetVal("value")
	runOne(t, hook, ctx, get)
	exists := redis.NewCmd(ctx, "script", "exists", testScriptSHA)
	exists.SetVal([]interface{}{int64(1)})
	runOne(t, hook, ctx, exists)
	metrics := recorder.phaseTwo.redisCalls
	if got := testutil.CollectAndCount(metrics.scriptCalls); got != 0 {
		t.Errorf("redis_script_total has %d series", got)
	}
	if got := testutil.CollectAndCount(metrics.scriptDuration); got != 0 {
		t.Errorf("redis_script_duration_seconds has %d series", got)
	}
	if got := testutil.CollectAndCount(metrics.scriptBytes); got != 0 {
		t.Errorf("redis_script_bytes_total has %d series", got)
	}
}

// A batch that starts with SCRIPT LOAD is timed under the script it carries, so
// taking the load away one day leaves this reading where it was.
func TestABatchIsTimedOnceUnderTheScriptItCarriesNotTheLoadInFrontOfIt(t *testing.T) {
	ctx := context.Background()
	recorder := NewRecorder(BuildInfo{})
	hook := recorder.RedisHook("runtime")
	var clock time.Time
	hook.now = func() time.Time { clock = clock.Add(10 * time.Millisecond); return clock }
	runBatch(t, hook, ctx, []redis.Cmder{
		scriptLoadCommand(ctx, testScriptBody),
		evalShaCommand(ctx, testScriptSHA, nil, int64(1)),
		evalShaCommand(ctx, testScriptSHA, nil, int64(1)),
	})
	metrics := recorder.phaseTwo.redisCalls
	if got := testutil.ToFloat64(metrics.scriptCalls.WithLabelValues(testScriptName, "load", "true", "ok")); got != 1 {
		t.Errorf("the load = %v", got)
	}
	if got := testutil.ToFloat64(metrics.scriptCalls.WithLabelValues(testScriptName, "evalsha", "true", "ok")); got != 2 {
		t.Errorf("the scripts = %v", got)
	}
	if got := redisHistogramCount(t, recorder, "alarmd_redis_script_duration_seconds", map[string]string{"script": testScriptName, "pipelined": "true"}); got != 1 {
		t.Errorf("the batch recorded %v times", got)
	}
	if got := redisHistogramSum(t, recorder, "alarmd_redis_script_duration_seconds", map[string]string{"script": testScriptName, "pipelined": "true"}); got != 0.01 {
		t.Errorf("the batch took %v, want one clock read after it was issued", got)
	}
	// The family it did not touch keeps its first-member reading.
	if got := redisHistogramCount(t, recorder, "alarmd_redis_command_duration_seconds", map[string]string{"client": "runtime", "command": "script", "pipelined": "true"}); got != 1 {
		t.Errorf("redis_command_duration_seconds under script = %v", got)
	}
	if got := redisHistogramCount(t, recorder, "alarmd_redis_command_duration_seconds", nil); got != 1 {
		t.Errorf("redis_command_duration_seconds records the batch %v times, once is what it says", got)
	}
	if got := redisHistogramCount(t, recorder, "alarmd_redis_caller_duration_seconds", map[string]string{"client": "runtime", "caller": redisCallerUnnamed, "pipelined": "true"}); got != 1 {
		t.Errorf("the batch's caller recorded %v times", got)
	}
}

func TestABatchWithoutAScriptOrWithOnlyALoadIsNotTimedAsOne(t *testing.T) {
	ctx := context.Background()
	recorder := NewRecorder(BuildInfo{})
	hook := recorder.RedisHook("runtime")
	runBatch(t, hook, ctx, []redis.Cmder{scriptLoadCommand(ctx, testScriptBody)})
	get := redis.NewStringCmd(ctx, "get", "key")
	get.SetVal("value")
	runBatch(t, hook, ctx, []redis.Cmder{get})
	if got := testutil.CollectAndCount(recorder.phaseTwo.redisCalls.scriptDuration); got != 0 {
		t.Errorf("redis_script_duration_seconds has %d series", got)
	}
}

// A single SCRIPT LOAD is counted and not timed as the script, as in a batch.
func TestASingleLoadIsNotTimedAsTheScript(t *testing.T) {
	ctx := context.Background()
	recorder := NewRecorder(BuildInfo{})
	hook := recorder.RedisHook("runtime")
	runOne(t, hook, ctx, scriptLoadCommand(ctx, testScriptBody))
	metrics := recorder.phaseTwo.redisCalls
	if got := testutil.ToFloat64(metrics.scriptCalls.WithLabelValues(testScriptName, "load", "false", "ok")); got != 1 {
		t.Errorf("the load = %v", got)
	}
	if got := testutil.CollectAndCount(metrics.scriptDuration); got != 0 {
		t.Errorf("redis_script_duration_seconds has %d series", got)
	}
}

// The hook has no business refusing a batch shape: a batch of two scripts is
// timed under the first, whatever the scan in the tests says about it.
func TestABatchOfTwoScriptsPassesThroughTheHook(t *testing.T) {
	ctx := context.Background()
	recorder := NewRecorder(BuildInfo{})
	runBatch(t, recorder.RedisHook("runtime"), ctx, []redis.Cmder{
		evalShaCommand(ctx, testScriptSHA, nil, int64(1)),
		evalShaCommand(ctx, redis.NewScript(testSecondBody).Hash(), nil, int64(1)),
	})
	if got := redisHistogramCount(t, recorder, "alarmd_redis_script_duration_seconds", map[string]string{"script": testScriptName, "pipelined": "true"}); got != 1 {
		t.Errorf("the first script recorded %v times", got)
	}
	if got := testutil.CollectAndCount(recorder.phaseTwo.redisCalls.scriptDuration); got != 1 {
		t.Errorf("%d series, want one recording of the batch", got)
	}
}

func TestOneOperationReadsTheClockOnceAfterIssuing(t *testing.T) {
	ctx := context.Background()
	recorder := NewRecorder(BuildInfo{})
	hook := recorder.RedisHook("runtime")
	reads := 0
	hook.now = func() time.Time { reads++; return time.Unix(int64(reads), 0) }
	runOne(t, hook, ctx, evalShaCommand(ctx, testScriptSHA, nil, int64(1)))
	if reads != 2 {
		t.Errorf("a call read the clock %d times, want one issuing it and one when it came back", reads)
	}
	for _, name := range []string{"alarmd_redis_script_duration_seconds", "alarmd_redis_caller_duration_seconds", "alarmd_redis_command_duration_seconds"} {
		if got := redisHistogramSum(t, recorder, name, nil); got != 1 {
			t.Errorf("%s took %v, want the one interval", name, got)
		}
	}
	reads = 0
	runBatch(t, hook, ctx, []redis.Cmder{evalShaCommand(ctx, testScriptSHA, nil, int64(1))})
	if reads != 2 {
		t.Errorf("a batch read the clock %d times, want two", reads)
	}
	// The value and not only the count: the fleet's flow reads the buckets.
	for _, name := range []string{"alarmd_redis_script_duration_seconds", "alarmd_redis_caller_duration_seconds", "alarmd_redis_command_duration_seconds"} {
		if got := redisHistogramSum(t, recorder, name, map[string]string{"pipelined": "true"}); got != 1 {
			t.Errorf("%s took %v for the batch, want the one interval", name, got)
		}
	}
}

// The bytes family is bounded by the command list as the others are: a command
// outside it must not mint a series under its own name.
func TestACommandOutsideTheListReadsAsOtherInTheBytesFamily(t *testing.T) {
	ctx := context.Background()
	recorder := NewRecorder(BuildInfo{})
	hook := recorder.RedisHook("runtime")
	unlisted := func() redis.Cmder {
		cmd := redis.NewStatusCmd(ctx, "debug", "object", "key")
		cmd.SetVal("OK")
		return cmd
	}
	runOne(t, hook, ctx, unlisted())
	runBatch(t, hook, ctx, []redis.Cmder{unlisted()})
	commands := redisLabelValues(t, recorder, "alarmd_redis_bytes_total", "command")
	if len(commands) != 2 {
		t.Fatalf("%d series, want sent and received: %v", len(commands), commands)
	}
	for _, command := range commands {
		if command != "other" {
			t.Errorf("the command label is %q", command)
		}
	}
	for _, direction := range []string{redisBytesSent, redisBytesReceived} {
		if got := redisCounterSum(t, recorder, "alarmd_redis_bytes_total", map[string]string{"command": "other", "direction": direction}); got == 0 {
			t.Errorf("%s counted nothing", direction)
		}
	}
}

func TestACallerReadsByItsNameAndNoNameReadsAsUnnamed(t *testing.T) {
	ctx := context.Background()
	recorder := NewRecorder(BuildInfo{})
	hook := recorder.RedisHook("runtime")
	named := redisfailure.WithCaller(ctx, redisfailure.CallerOwnership)
	unknown := redisfailure.WithCaller(ctx, "a job nobody listed")
	get := func() redis.Cmder {
		cmd := redis.NewStringCmd(ctx, "get", "key")
		cmd.SetVal("value")
		return cmd
	}
	runOne(t, hook, named, get())
	runOne(t, hook, ctx, get())
	runOne(t, hook, unknown, get())
	runBatch(t, hook, named, []redis.Cmder{get(), get()})
	for _, test := range []struct {
		caller, pipelined string
		want              float64
	}{
		{redisfailure.CallerOwnership, "false", 1}, {redisfailure.CallerOwnership, "true", 1},
		{redisCallerUnnamed, "false", 1}, {"other", "false", 1},
	} {
		if got := redisHistogramCount(t, recorder, "alarmd_redis_caller_duration_seconds",
			map[string]string{"client": "runtime", "caller": test.caller, "pipelined": test.pipelined}); got != test.want {
			t.Errorf("%s pipelined=%s = %v, want %v", test.caller, test.pipelined, got, test.want)
		}
	}
	// The two sums the family promises.
	metrics := recorder.phaseTwo.redisCalls
	if total, operations := redisHistogramCount(t, recorder, "alarmd_redis_caller_duration_seconds", map[string]string{"client": "runtime"}),
		testutil.ToFloat64(metrics.operations.WithLabelValues("runtime")); total != operations {
		t.Errorf("caller duration count = %v, redis_operation_total = %v", total, operations)
	}
	if named, operations := redisHistogramCount(t, recorder, "alarmd_redis_caller_duration_seconds", map[string]string{"client": "runtime", "caller": redisfailure.CallerOwnership}),
		testutil.ToFloat64(metrics.callerOperations.WithLabelValues("runtime", redisfailure.CallerOwnership)); named != operations {
		t.Errorf("named caller duration count = %v, redis_caller_operation_total = %v", named, operations)
	}
}

// A request the pool never sent is not traffic: counting it would show bytes
// in exactly the window where none moved.
func TestBytesCountOnlyWhatTheServerAnswered(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		name    string
		err     error
		counted bool
	}{
		{"a reply", nil, true},
		{"an empty reply", redis.Nil, true},
		{"a cold cache", replyError("NOSCRIPT No matching script"), true},
		{"an error reply", replyError("ERR boom"), true},
		{"a pool timeout", errors.New("redis: connection pool timeout"), false},
		{"a dial failure", errors.New("dial tcp 192.0.2.1:6379: connect: connection refused"), false},
		{"a cancelled call", context.Canceled, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := NewRecorder(BuildInfo{})
			runOne(t, recorder.RedisHook("runtime"), ctx, evalShaCommand(ctx, testScriptSHA, test.err, int64(1)))
			metrics := recorder.phaseTwo.redisCalls
			sent := testutil.ToFloat64(metrics.bytes.WithLabelValues("runtime", redisCallerUnnamed, "evalsha", "sent"))
			received := testutil.ToFloat64(metrics.bytes.WithLabelValues("runtime", redisCallerUnnamed, "evalsha", "received"))
			scriptSent := testutil.ToFloat64(metrics.scriptBytes.WithLabelValues(testScriptName, "sent"))
			scriptReceived := testutil.ToFloat64(metrics.scriptBytes.WithLabelValues(testScriptName, "received"))
			if test.counted && (sent == 0 || received == 0 || scriptSent != sent || scriptReceived != received) {
				t.Errorf("sent %v received %v, by script %v and %v", sent, received, scriptSent, scriptReceived)
			}
			if !test.counted && (sent != 0 || received != 0 || scriptSent != 0 || scriptReceived != 0) {
				t.Errorf("sent %v received %v, by script %v and %v, want nothing", sent, received, scriptSent, scriptReceived)
			}
		})
	}
}

type marshalledArgument string

func (argument marshalledArgument) MarshalBinary() ([]byte, error) { return []byte(argument), nil }

func TestSentBytesAreTheCommandAsGoRedisWritesIt(t *testing.T) {
	for _, test := range []struct {
		args []interface{}
		want string
	}{
		{[]interface{}{"get", "k"}, "*2\r\n$3\r\nget\r\n$1\r\nk\r\n"},
		{[]interface{}{"x", nil}, "*2\r\n$1\r\nx\r\n$0\r\n\r\n"},
		{[]interface{}{"x", []byte("ab")}, "*2\r\n$1\r\nx\r\n$2\r\nab\r\n"},
		{[]interface{}{"x", 12345}, "*2\r\n$1\r\nx\r\n$5\r\n12345\r\n"},
		{[]interface{}{"x", int64(-7)}, "*2\r\n$1\r\nx\r\n$2\r\n-7\r\n"},
		{[]interface{}{"x", uint64(1000)}, "*2\r\n$1\r\nx\r\n$4\r\n1000\r\n"},
		{[]interface{}{"x", 1.5}, "*2\r\n$1\r\nx\r\n$3\r\n1.5\r\n"},
		{[]interface{}{"x", true}, "*2\r\n$1\r\nx\r\n$1\r\n1\r\n"},
		{[]interface{}{"x", time.Millisecond}, "*2\r\n$1\r\nx\r\n$7\r\n1000000\r\n"},
		{[]interface{}{"x", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}, "*2\r\n$1\r\nx\r\n$20\r\n2026-01-02T03:04:05Z\r\n"},
		{[]interface{}{"x", marshalledArgument("abc")}, "*2\r\n$1\r\nx\r\n$3\r\nabc\r\n"},
	} {
		if got := sentBytes(test.args); got != len(test.want) {
			t.Errorf("%v: %d bytes, want %d", test.args, got, len(test.want))
		}
	}
}
