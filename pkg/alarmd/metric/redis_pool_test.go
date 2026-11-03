// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func collectRedisPool(t *testing.T, counts []RedisPoolCounts) []string {
	t.Helper()
	collector := newRedisPoolCollector()
	collector.source = func() []RedisPoolCounts { return counts }
	channel := make(chan prometheus.Metric, 64)
	collector.Collect(channel)
	close(channel)
	var rendered []string
	for metric := range channel {
		rendered = append(rendered, metric.Desc().String())
	}
	sort.Strings(rendered)
	return rendered
}

func TestRedisPoolCollectorEmitsOccupancyAndWaitOutcomes(t *testing.T) {
	rendered := collectRedisPool(t, []RedisPoolCounts{{
		Client: "source", Size: 80, TotalConns: 12, IdleConns: 4, StaleConns: 1,
		Hits: 900, Misses: 30, Timeouts: 2,
	}})
	if len(rendered) != 7 {
		t.Fatalf("expected one size, three connection states and three wait outcomes, got %d: %v", len(rendered), rendered)
	}
	var size, connections, waits int
	for _, descriptor := range rendered {
		switch {
		case strings.Contains(descriptor, "redis_pool_size"):
			size++
		case strings.Contains(descriptor, "redis_pool_connections"):
			connections++
		case strings.Contains(descriptor, "redis_pool_waits_total"):
			waits++
		}
	}
	if size != 1 || connections != 3 || waits != 3 {
		t.Fatalf("unexpected series split: size=%d connections=%d waits=%d", size, connections, waits)
	}
}

// A client without a name would produce series that cannot be attributed, so it
// is dropped rather than reported under an empty label.
func TestRedisPoolCollectorSkipsUnnamedClients(t *testing.T) {
	if rendered := collectRedisPool(t, []RedisPoolCounts{{Client: "", Size: 16}}); len(rendered) != 0 {
		t.Fatalf("unnamed client produced series: %v", rendered)
	}
}

func TestRedisPoolCollectorWithoutSourceIsSilent(t *testing.T) {
	collector := newRedisPoolCollector()
	channel := make(chan prometheus.Metric, 4)
	collector.Collect(channel)
	close(channel)
	if _, ok := <-channel; ok {
		t.Fatal("collector emitted a series before a source was bound")
	}
}
