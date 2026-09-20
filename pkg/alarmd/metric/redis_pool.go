// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// The connection pool was the narrowest concurrency gate in the process and
// nothing reported it: command duration absorbed the queueing, so a saturated
// pool looked like a slow Redis. These series make the pool the one gate that
// states its own occupancy - the configured size next to the connections in
// use, and the wait outcomes that say whether callers found a connection or
// queued for one.
type RedisPoolCounts struct {
	Client     string
	Size       int
	TotalConns uint32
	IdleConns  uint32
	StaleConns uint32
	Hits       uint32
	Misses     uint32
	Timeouts   uint32
}

type redisPoolCollector struct {
	mu          sync.Mutex
	source      func() []RedisPoolCounts
	size        *prometheus.Desc
	connections *prometheus.Desc
	waits       *prometheus.Desc
}

func newRedisPoolCollector() *redisPoolCollector {
	return &redisPoolCollector{
		size: prometheus.NewDesc(
			prometheus.BuildFQName(metricNamespace, metricSubsystem, "redis_pool_size"),
			"Configured Redis connection pool size by client.",
			[]string{"client"}, nil,
		),
		connections: prometheus.NewDesc(
			prometheus.BuildFQName(metricNamespace, metricSubsystem, "redis_pool_connections"),
			"Redis connection pool occupancy by client and connection state.",
			[]string{"client", "state"}, nil,
		),
		waits: prometheus.NewDesc(
			prometheus.BuildFQName(metricNamespace, metricSubsystem, "redis_pool_waits_total"),
			"Redis connection pool acquisitions by client and outcome.",
			[]string{"client", "result"}, nil,
		),
	}
}

// SetRedisPoolSource binds the collector to the live client statistics. A nil
// recorder is a no-op so wiring never has to be ordered against construction.
func (r *Recorder) SetRedisPoolSource(source func() []RedisPoolCounts) {
	if r == nil || r.phaseTwo.redisPool == nil {
		return
	}
	r.phaseTwo.redisPool.mu.Lock()
	r.phaseTwo.redisPool.source = source
	r.phaseTwo.redisPool.mu.Unlock()
}

func (c *redisPoolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.size
	ch <- c.connections
	ch <- c.waits
}

func (c *redisPoolCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	if source == nil {
		return
	}
	for _, counts := range source() {
		if counts.Client == "" {
			continue
		}
		ch <- prometheus.MustNewConstMetric(c.size, prometheus.GaugeValue, float64(counts.Size), counts.Client)
		ch <- prometheus.MustNewConstMetric(c.connections, prometheus.GaugeValue, float64(counts.TotalConns), counts.Client, "total")
		ch <- prometheus.MustNewConstMetric(c.connections, prometheus.GaugeValue, float64(counts.IdleConns), counts.Client, "idle")
		ch <- prometheus.MustNewConstMetric(c.connections, prometheus.GaugeValue, float64(counts.StaleConns), counts.Client, "stale")
		ch <- prometheus.MustNewConstMetric(c.waits, prometheus.CounterValue, float64(counts.Hits), counts.Client, "hit")
		ch <- prometheus.MustNewConstMetric(c.waits, prometheus.CounterValue, float64(counts.Misses), counts.Client, "miss")
		ch <- prometheus.MustNewConstMetric(c.waits, prometheus.CounterValue, float64(counts.Timeouts), counts.Client, "timeout")
	}
}
