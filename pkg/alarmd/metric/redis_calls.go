// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus"
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
	"source": {}, "runtime": {}, "cmdb": {}, "legacy_output": {}, "legacy_pod_cache": {}, "diagnostics": {},
}

var redisCommandNames = map[string]struct{}{
	"get": {}, "mget": {}, "set": {}, "setex": {}, "psetex": {}, "del": {}, "exists": {},
	"eval": {}, "evalsha": {}, "script": {},
	"hget": {}, "hmget": {}, "hgetall": {}, "hset": {}, "hdel": {}, "hlen": {},
	"expire": {}, "pexpire": {}, "ttl": {}, "pttl": {}, "strlen": {}, "incrby": {},
	"zadd": {}, "zcard": {}, "zrange": {}, "zrangebyscore": {}, "zrem": {}, "zremrangebyrank": {},
	"smembers": {}, "sadd": {}, "srem": {}, "scan": {}, "hscan": {},
	"multi": {}, "exec": {}, "ping": {}, "select": {}, "info": {}, "unlink": {},
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
}

func newRedisCallMetrics() redisCallMetrics {
	labels := []string{"client", "command", "pipelined"}
	operations := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "redis_operation_total",
		Help: "Redis operations issued, counting one per call or pipeline batch rather than per command.",
	}, []string{"client"})
	return redisCallMetrics{
		operations: operations,
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
			Help: "Redis commands that returned an error, excluding the empty-result signal.",
		}, labels),
	}
}

func (m redisCallMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{m.calls, m.duration, m.failures, m.operations}
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
}

// RedisHook returns the recorder's Redis instrumentation for one named client.
// A nil recorder yields nil so callers can wire it unconditionally.
func (r *Recorder) RedisHook(client string) *RedisCallHook {
	if r == nil {
		return nil
	}
	return &RedisCallHook{metrics: r.phaseTwo.redisCalls, client: boundedRedisClient(client), now: time.Now}
}

type redisCallStartKey struct{}

func (h *RedisCallHook) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	if h == nil {
		return ctx, nil
	}
	h.metrics.operations.WithLabelValues(h.client).Inc()
	return context.WithValue(ctx, redisCallStartKey{}, h.now()), nil
}

func (h *RedisCallHook) AfterProcess(ctx context.Context, cmd redis.Cmder) error {
	if h == nil {
		return nil
	}
	h.record(ctx, boundedRedisCommand(cmd.Name()), "false", cmd.Err())
	return nil
}

func (h *RedisCallHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	if h == nil {
		return ctx, nil
	}
	h.metrics.operations.WithLabelValues(h.client).Inc()
	return context.WithValue(ctx, redisCallStartKey{}, h.now()), nil
}

// AfterProcessPipeline counts every member of the batch, because the Redis
// server executes and is limited by each of them, and records the elapsed
// time once against the first member so the histogram keeps meaning
// "round trip" rather than "per command inside a batch".
func (h *RedisCallHook) AfterProcessPipeline(ctx context.Context, cmds []redis.Cmder) error {
	if h == nil {
		return nil
	}
	for index, cmd := range cmds {
		name := boundedRedisCommand(cmd.Name())
		h.metrics.calls.WithLabelValues(h.client, name, "true").Inc()
		if err := cmd.Err(); err != nil && err != redis.Nil {
			h.metrics.failures.WithLabelValues(h.client, name, "true").Inc()
		}
		if index == 0 {
			h.observeDuration(ctx, name, "true")
		}
	}
	return nil
}

func (h *RedisCallHook) record(ctx context.Context, name, pipelined string, err error) {
	h.metrics.calls.WithLabelValues(h.client, name, pipelined).Inc()
	if err != nil && err != redis.Nil {
		h.metrics.failures.WithLabelValues(h.client, name, pipelined).Inc()
	}
	h.observeDuration(ctx, name, pipelined)
}

func (h *RedisCallHook) observeDuration(ctx context.Context, name, pipelined string) {
	started, ok := ctx.Value(redisCallStartKey{}).(time.Time)
	if !ok {
		return
	}
	h.metrics.duration.WithLabelValues(h.client, name, pipelined).Observe(h.now().Sub(started).Seconds())
}
