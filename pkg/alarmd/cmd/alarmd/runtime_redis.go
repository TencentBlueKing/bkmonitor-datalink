// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package main

import (
	"context"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
)

func openProductionRedis(ctx context.Context, connection config.RedisConnectionConfig) (redis.UniversalClient, error) {
	return openProductionRedisWithHook(ctx, connection, nil)
}

// openProductionRedisWithHook attaches the command recorder before the first
// command so the per-command counts cover the whole process lifetime, Ping
// included. A nil hook keeps the client uninstrumented.
func openProductionRedisWithHook(
	ctx context.Context, connection config.RedisConnectionConfig, hook *metric.RedisCallHook,
) (redis.UniversalClient, error) {
	return openProductionRedisOptionsWithHook(ctx, productionRedisOptions(connection), hook)
}

func openProductionRedisOptionsWithHook(ctx context.Context, options *redis.UniversalOptions, hook *metric.RedisCallHook) (redis.UniversalClient, error) {
	client := redis.NewUniversalClient(options)
	if hook != nil {
		client.AddHook(hook)
	}
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func productionRedisOptions(connection config.RedisConnectionConfig) *redis.UniversalOptions {
	addresses := []string{connection.Address}
	masterName := ""
	if connection.Mode == config.RedisModeSentinel {
		addresses = append([]string(nil), connection.SentinelAddress...)
		masterName = connection.MasterName
	}
	return &redis.UniversalOptions{
		Addrs: addresses, MasterName: masterName,
		Username: connection.Username, Password: connection.Password,
		SentinelUsername: connection.SentinelUsername, SentinelPassword: connection.SentinelPassword,
		DB: connection.DB, DialTimeout: connection.DialTimeout.Duration(), ReadTimeout: connection.ReadTimeout.Duration(),
		WriteTimeout: connection.WriteTimeout.Duration(),
		PoolSize:     connection.EffectivePoolSize(0),
		// One attempt. The scheduler already owns retrying: a Go error from
		// Execute records a failed attempt of the frozen Slot, which is parked
		// in the delayed queue with exponential backoff and no attempt cap, and
		// the retry re-reads current state rather than resending the same
		// bytes. The client's own retries add no reliability to that - the same
		// request goes to the same server - and they multiply what it costs:
		// the default three turns a 3 s read timeout into 12 s.
		//
		// Twelve seconds is the damage, and it is paid twice. The worker is
		// held that long on a read that fails identically every attempt, and on
		// a short-period Plan it is most of the completion window, so the Slot
		// spends its budget waiting rather than evaluating. The retry itself
		// still happens either way: the context executionErrorBacksOff judges
		// is the runner's, and the completion deadline is derived inside the
		// coordinator and does not reach it.
		MaxRetries: -1,
	}
}

func productionRedisAddress(connection config.RedisConnectionConfig) string {
	if connection.Mode == config.RedisModeSentinel {
		return "sentinel:" + connection.MasterName
	}
	return connection.Address
}

// redisPoolCounts reads the live pool statistics for one client. The size is
// the resolved configuration value rather than a statistic, so a saturated pool
// can be read straight off the ratio instead of being inferred from latency.
func redisPoolCounts(client string, size int, redisClient redis.UniversalClient) metric.RedisPoolCounts {
	counts := metric.RedisPoolCounts{Client: client, Size: size}
	if redisClient == nil {
		return counts
	}
	stats := redisClient.PoolStats()
	if stats == nil {
		return counts
	}
	counts.TotalConns = stats.TotalConns
	counts.IdleConns = stats.IdleConns
	counts.StaleConns = stats.StaleConns
	counts.Hits = stats.Hits
	counts.Misses = stats.Misses
	counts.Timeouts = stats.Timeouts
	return counts
}
