// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package main

import (
	"context"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
)

func openProductionRedis(ctx context.Context, connection config.RedisConnectionConfig) (redis.UniversalClient, error) {
	client := redis.NewUniversalClient(productionRedisOptions(connection))
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
		WriteTimeout: connection.WriteTimeout.Duration(), PoolSize: connection.PoolSize,
	}
}

func productionRedisAddress(connection config.RedisConnectionConfig) string {
	if connection.Mode == config.RedisModeSentinel {
		return "sentinel:" + connection.MasterName
	}
	return connection.Address
}
