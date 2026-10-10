// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
)

// Redis load is the one budget nobody can currently attribute: the instance
// command rate moves with Slot throughput, but window differencing gives
// slopes that disagree by an order of magnitude and names commands the hot
// path does not issue. These metrics answer it from inside the process, by
// command and by whether the command was pipelined, so "commands per Slot"
// stops being an inference.
//
// Cardinality is bounded by redisCommandNames: anything outside it collapses
// to "other", the same discipline the rest of the phase-two metrics follow.
//
// The client dimension answers a question the totals cannot: this process opens
// several Redis clients for different jobs, and "the command rate went up" is
// unactionable until it says which of them. It is what lets the diagnostics
// traffic be told apart from the control plane traffic the pools were sized
// for, which is the whole premise of keeping those clients separate.
// redisClientNames is closed for the same reason the command list is: a client
// name is chosen in wiring code, and a typo there would otherwise mint a new
// series nobody notices. The values match the pool metrics so command load and
// connection load join on the same label.
var redisClientNames = map[string]struct{}{
	"source": {}, "runtime": {}, "cmdb": {}, "dynamic_config": {}, "target_group": {}, "legacy_output": {}, "legacy_pod_cache": {}, "diagnostics": {},
	"linkd": {},
}

// RedisClientHealth is what this process has last seen of one Redis client:
// when a command last completed, when one last failed, and what the failure
// said. It is the reading the fleet page gives beside the client's address,
// because an address alone cannot tell a Redis that answers from one that
// does not, and the counters beside it say how often, never how recently.
type RedisClientHealth struct {
	LastSuccessAt time.Time
	LastFailureAt time.Time
	// LastFailure is the error's text, sanitised of anything that looks like a
	// credential and bounded. Empty until one has failed.
	LastFailure string
	// ScriptCacheMisses counts the NOSCRIPT replies: EVALSHA named a script
	// the server has not cached, and the caller's next step is to send the
	// body with EVAL. It is the one error reply that is not a failure of
	// anything -- it is how a script gets loaded after this process, the
	// server or the sentinel's master changes -- and it was sitting in
	// LastFailure on a deployment's dependency table for as long as nothing
	// else failed, which read as a Redis with a problem. Kept apart, with
	// its own clock: a miss long after start is a server that lost its
	// cache, which is a restart or a failover this process did not otherwise
	// see.
	ScriptCacheMisses     int
	LastScriptCacheMissAt time.Time
}

// noScriptReply is the reply that means the script body has to be sent again,
// matched strictly on the server's own word so that no other error is read
// as it. The same predicate the state backend retries on.
func noScriptReply(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "NOSCRIPT")
}

// redisClientHealthTextLimit bounds the failure text kept. Long enough for a
// dial error with its address and reason; short enough that a client returning
// a payload as its error does not fill the snapshot.
const redisClientHealthTextLimit = 256

// redisClientHealthBook is the per-client record, shared by every hook the
// Recorder hands out so that a role served by a reused connection reads the
// connection's health and not an empty one.
type redisClientHealthBook struct {
	mu      sync.Mutex
	clients map[string]*RedisClientHealth
}

func (book *redisClientHealthBook) note(client string, at time.Time, err error) {
	if book == nil {
		return
	}
	book.mu.Lock()
	defer book.mu.Unlock()
	if book.clients == nil {
		book.clients = map[string]*RedisClientHealth{}
	}
	health := book.clients[client]
	if health == nil {
		health = &RedisClientHealth{}
		book.clients[client] = health
	}
	if err == nil || err == redis.Nil {
		health.LastSuccessAt = at
		return
	}
	if noScriptReply(err) {
		health.ScriptCacheMisses++
		health.LastScriptCacheMissAt = at
		return
	}
	health.LastFailureAt = at
	text := sanitizeRedisError(err.Error())
	if len(text) > redisClientHealthTextLimit {
		text = text[:redisClientHealthTextLimit] + "..."
	}
	health.LastFailure = text
}

func (book *redisClientHealthBook) read(client string) (RedisClientHealth, bool) {
	if book == nil {
		return RedisClientHealth{}, false
	}
	book.mu.Lock()
	defer book.mu.Unlock()
	health := book.clients[client]
	if health == nil {
		return RedisClientHealth{}, false
	}
	return *health, true
}

// sanitizeRedisError strips what a Redis error can carry that a page must not
// show: a URL-form address with credentials in it. Dial errors name the host
// and port, which the page shows anyway beside the client; a password never
// appears in go-redis's own errors, but an application error wrapping the
// options might, and the rule is cheaper than the audit.
func sanitizeRedisError(text string) string {
	if at := strings.Index(text, "@"); at >= 0 {
		if scheme := strings.LastIndex(text[:at], "://"); scheme >= 0 {
			return text[:scheme+3] + "***" + text[at:]
		}
	}
	return text
}

var redisCommandNames = map[string]struct{}{
	"get": {}, "mget": {}, "set": {}, "setex": {}, "psetex": {}, "del": {}, "exists": {},
	"eval": {}, "evalsha": {}, "script": {},
	"hget": {}, "hmget": {}, "hgetall": {}, "hset": {}, "hdel": {}, "hlen": {},
	"expire": {}, "pexpire": {}, "ttl": {}, "pttl": {}, "strlen": {}, "incrby": {},
	"zadd": {}, "zcard": {}, "zrange": {}, "zrangebyscore": {}, "zrem": {}, "zremrangebyrank": {},
	"smembers": {}, "sadd": {}, "srem": {}, "scan": {}, "hscan": {},
	"multi": {}, "exec": {}, "ping": {}, "select": {}, "info": {}, "unlink": {},
	// What the observation jobs on the diagnostics client issue: bounded
	// reads of a published value and of a record list, and the list writes.
	// Outside the list they all read as "other", which is where every one of
	// that client's failures sat.
	"getrange": {}, "lrange": {}, "lpush": {}, "ltrim": {},
}

type redisCallMetrics struct {
	calls    *prometheus.CounterVec
	duration *prometheus.HistogramVec
	failures *prometheus.CounterVec
	// The client retries a failed attempt up to three times with an eight
	// millisecond floor on the backoff, and the whole retry loop sits inside the
	// call this hook times. Retries therefore inflate the recorded duration
	// while leaving no trace of their own: only the final outcome reaches
	// failures. Counting operations makes them visible, because the connection
	// pool counts one acquisition per attempt, so attempts minus operations is
	// the number of retries.
	operations *prometheus.CounterVec
	// reasons says why the operations of a client failed. failures says which
	// command, and on the runtime and control plane clients that was all there
	// was: a timeout, a connection cut while idle and a Sentinel with no master
	// read the same. It counts operations, as operations does, so the two
	// divide into a failure rate per client; a pipeline is one operation and
	// its first failing member decides the reason. The words are the ones the
	// diagnostic clients are counted by (redisfailure), so the two readings
	// join.
	reasons *prometheus.CounterVec
	// callerOperations and callerReasons are operations and reasons again,
	// by the job that made the call (redisfailure.Callers), for calls whose
	// context names one. Several observation jobs share the diagnostics
	// client, and its failures by reason could not say whose they were.
	callerOperations *prometheus.CounterVec
	callerReasons    *prometheus.CounterVec
}

func newRedisCallMetrics() redisCallMetrics {
	labels := []string{"client", "command", "pipelined"}
	operations := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "redis_operation_total",
		Help: "Redis operations issued, counting one per call or pipeline batch rather than per command.",
	}, []string{"client"})
	reasons := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "redis_failure_reason_total",
		Help: "Redis operations that failed, by client and by why, one per call or pipeline batch; a batch's first " +
			"failing member decides the reason: connection_closed, connection_refused, sentinel_unreachable, timeout, " +
			"pool_timeout, canceled, server_error, malformed_reply, other. The empty-result signal and a NOSCRIPT " +
			"reply are not failures and are not counted. Every cell exists from startup, so a zero is a count.",
	}, []string{"client", "reason"})
	for client := range redisClientNames {
		for _, reason := range redisfailure.Reasons {
			reasons.WithLabelValues(client, reason)
		}
	}
	for _, reason := range redisfailure.Reasons {
		reasons.WithLabelValues("other", reason)
	}
	callerOperations := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "redis_caller_operation_total",
		Help: "Redis operations issued by a named job on a client, one per call or pipeline batch, for the jobs that " +
			"share one client: on the diagnostics client directory_read, diagnostic_write, " +
			"diagnostic_read, cost_projection; on the source and runtime clients (one client when the deployment " +
			"points both at one Redis) strategy_source, legacy_effective_time, control_plane, ownership, runtime_state, " +
			"query_cooldown, fleet, linkd; cmdb_cache, target_group and dynamic_config on whichever client they share; " +
			"store_census on the source and runtime clients.",
	}, []string{"client", "caller"})
	callerReasons := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "redis_caller_failure_reason_total",
		Help: "Redis operations of a named job that failed, by why, counted as redis_failure_reason_total is. The " +
			"diagnostics client's cells exist from startup, so a zero is a count.",
	}, []string{"client", "caller", "reason"})
	for _, caller := range redisfailure.Callers {
		callerOperations.WithLabelValues("diagnostics", caller)
		for _, reason := range redisfailure.Reasons {
			callerReasons.WithLabelValues("diagnostics", caller, reason)
		}
	}
	return redisCallMetrics{
		operations: operations,
		reasons:    reasons,
		calls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "redis_command_total",
			Help: "Redis commands issued by this process by bounded command name and pipelining.",
		}, labels),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "redis_command_duration_seconds",
			Help:    "Redis round-trip duration. A pipelined batch is recorded once for the whole batch.",
			Buckets: []float64{0.0005, 0.001, 0.002, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
		}, labels),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "redis_command_failure_total",
			Help: "Redis commands that returned an error, excluding the empty-result signal. A NOSCRIPT reply to EVALSHA is counted here as the error reply it is, and is not a dependency failure: the client answers it with EVAL, and the fleet page keeps it apart as a script cache miss.",
		}, labels),
		callerOperations: callerOperations, callerReasons: callerReasons,
	}
}

func (m redisCallMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{m.calls, m.duration, m.failures, m.operations, m.reasons, m.callerOperations, m.callerReasons}
}

// failureReason is why an operation failed, or "" when it did not. The
// empty-result signal is an answer, and a NOSCRIPT reply is a script cache
// miss the caller answers with EVAL; neither is the dependency failing.
func failureReason(err error) string {
	if err == nil || err == redis.Nil || noScriptReply(err) {
		return ""
	}
	return redisfailure.Reason(err)
}

// callFailureReason is failureReason for an operation made under ctx. A call
// issued with its caller's deadline already past failed on the caller's
// clock, whatever it says: the dialer and go-redis set every connection's
// deadline from ctx.Deadline(), so each dial fails at once, and asking each
// Sentinel that way go-redis gives up in the words of a Sentinel outage - an
// operation that never reached the network read as Sentinels down. It is
// named by the context: canceled for a cancelled caller, timeout otherwise,
// including the moment a deadline has passed and the context's timer has not
// yet fired, when ctx.Err() is still nil. A call that failed with time left
// keeps its own words, sentinel_unreachable included: a Sentinel that hangs
// past a caller's deadline shorter than the read timeout is an outage, not
// the caller's clock. What no rule tells apart: go-redis's pool, after
// PoolSize dial failures, answers new callers with the last dial error until
// a redial succeeds, on contexts that still have time - milliseconds while
// the Sentinels are well.
func callFailureReason(ctx context.Context, err error) string {
	reason := failureReason(err)
	if reason == "" {
		return reason
	}
	if start, ok := ctx.Value(redisCallStartKey{}).(redisCallStart); ok && start.spent {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return redisfailure.Reason(ctxErr)
		}
		return redisfailure.Timeout
	}
	return reason
}

// spentAtIssue says ctx gave its call no time: cancelled, or its deadline
// reached by the wall clock the dialer checks it against.
func spentAtIssue(ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	deadline, ok := ctx.Deadline()
	return ok && !time.Now().Before(deadline)
}

func boundedRedisCommand(name string) string {
	name = strings.ToLower(name)
	if _, ok := redisCommandNames[name]; ok {
		return name
	}
	return "other"
}

func boundedRedisClient(name string) string {
	if _, ok := redisClientNames[name]; ok {
		return name
	}
	return "other"
}

// RedisCallHook records every command the runtime client issues. It is the
// only place that sees pipelined members individually, which matters because
// the shared Redis master is billed per command, not per round trip.
type RedisCallHook struct {
	metrics redisCallMetrics
	client  string
	now     func() time.Time
	health  *redisClientHealthBook
}

// RedisHook returns the recorder's Redis instrumentation for one named client.
// A nil recorder yields nil so callers can wire it unconditionally.
func (r *Recorder) RedisHook(client string) *RedisCallHook {
	if r == nil {
		return nil
	}
	return &RedisCallHook{metrics: r.phaseTwo.redisCalls, client: boundedRedisClient(client), now: time.Now,
		health: r.phaseTwo.redisHealth}
}

// RedisClientHealth reads what the hooks have recorded for one client name.
// False for a name no hook has reported on yet, which is a different answer
// from a client that has never failed.
func (r *Recorder) RedisClientHealth(client string) (RedisClientHealth, bool) {
	if r == nil {
		return RedisClientHealth{}, false
	}
	return r.phaseTwo.redisHealth.read(boundedRedisClient(client))
}

type redisCallStartKey struct{}

// redisCallStart is when an operation was issued, and whether its caller's
// context gave it no time then (spentAtIssue).
type redisCallStart struct {
	at    time.Time
	spent bool
}

func (h *RedisCallHook) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	if h == nil {
		return ctx, nil
	}
	h.metrics.operations.WithLabelValues(h.client).Inc()
	return context.WithValue(ctx, redisCallStartKey{}, redisCallStart{at: h.now(), spent: spentAtIssue(ctx)}), nil
}

// AfterProcess counts the operation for its caller here rather than before:
// a client's hooks run in the order added, and a caller a later hook names
// (the bundle's per-job clones) is in the context only from then on.
func (h *RedisCallHook) AfterProcess(ctx context.Context, cmd redis.Cmder) error {
	if h == nil {
		return nil
	}
	h.callerOperation(ctx)
	h.record(ctx, boundedRedisCommand(cmd.Name()), "false", cmd.Err())
	return nil
}

func (h *RedisCallHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	if h == nil {
		return ctx, nil
	}
	h.metrics.operations.WithLabelValues(h.client).Inc()
	return context.WithValue(ctx, redisCallStartKey{}, redisCallStart{at: h.now(), spent: spentAtIssue(ctx)}), nil
}

// AfterProcessPipeline counts every member of the batch, because the Redis
// server executes and is limited by each of them, and records the elapsed
// time once against the first member so the histogram keeps meaning
// "round trip" rather than "per command inside a batch".
func (h *RedisCallHook) AfterProcessPipeline(ctx context.Context, cmds []redis.Cmder) error {
	if h == nil {
		return nil
	}
	h.callerOperation(ctx)
	var failed error
	reason := ""
	for index, cmd := range cmds {
		name := boundedRedisCommand(cmd.Name())
		h.metrics.calls.WithLabelValues(h.client, name, "true").Inc()
		if err := cmd.Err(); err != nil && err != redis.Nil {
			h.metrics.failures.WithLabelValues(h.client, name, "true").Inc()
			if failed == nil {
				failed = err
			}
		}
		if reason == "" {
			reason = callFailureReason(ctx, cmd.Err())
		}
		if index == 0 {
			h.observeDuration(ctx, name, "true")
		}
	}
	if reason != "" {
		h.metrics.reasons.WithLabelValues(h.client, reason).Inc()
		h.callerFailure(ctx, reason)
	}
	h.health.note(h.client, h.now(), failed)
	return nil
}

// callerOperation and callerFailure count an operation, and its failure, for
// the job its context names; nothing for a call that names none.
func (h *RedisCallHook) callerOperation(ctx context.Context) {
	if caller := redisfailure.Caller(ctx); caller != "" {
		h.metrics.callerOperations.WithLabelValues(h.client, caller).Inc()
	}
}

func (h *RedisCallHook) callerFailure(ctx context.Context, reason string) {
	if caller := redisfailure.Caller(ctx); caller != "" {
		h.metrics.callerReasons.WithLabelValues(h.client, caller, reason).Inc()
	}
}

func (h *RedisCallHook) record(ctx context.Context, name, pipelined string, err error) {
	h.metrics.calls.WithLabelValues(h.client, name, pipelined).Inc()
	if err != nil && err != redis.Nil {
		h.metrics.failures.WithLabelValues(h.client, name, pipelined).Inc()
	}
	if reason := callFailureReason(ctx, err); reason != "" {
		h.metrics.reasons.WithLabelValues(h.client, reason).Inc()
		h.callerFailure(ctx, reason)
	}
	h.health.note(h.client, h.now(), err)
	h.observeDuration(ctx, name, pipelined)
}

func (h *RedisCallHook) observeDuration(ctx context.Context, name, pipelined string) {
	started, ok := ctx.Value(redisCallStartKey{}).(redisCallStart)
	if !ok {
		return
	}
	h.metrics.duration.WithLabelValues(h.client, name, pipelined).Observe(h.now().Sub(started.at).Seconds())
}
