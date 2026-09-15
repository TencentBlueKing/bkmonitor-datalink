// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"context"

	"github.com/go-redis/redis/v8"
)

// scopeRedis serves a few keys out of a map and counts the reads, for tests
// that want to know how many bodies a cache avoided without a Redis.
type scopeRedis struct {
	redis.Cmdable
	values               map[string]string
	gets, mgets, strlens int
	roundTrips           int
	getError             error
}

func (c *scopeRedis) Get(ctx context.Context, k string) *redis.StringCmd {
	c.gets++
	if c.getError != nil {
		return redis.NewStringResult("", c.getError)
	}
	v, ok := c.values[k]
	if !ok {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(v, ctx.Err())
}

func (c *scopeRedis) StrLen(ctx context.Context, k string) *redis.IntCmd {
	c.strlens++
	if c.getError != nil {
		return redis.NewIntResult(0, c.getError)
	}
	return redis.NewIntResult(int64(len(c.values[k])), ctx.Err())
}

// scopePipeline lets a batch reach the same values and the same per-command
// counters. roundTrips counts batches: the version read sends GET and STRLEN
// together, so it costs one.
type scopePipeline struct {
	redis.Pipeliner
	client *scopeRedis
}

func (pipe *scopePipeline) Get(ctx context.Context, k string) *redis.StringCmd {
	return pipe.client.Get(ctx, k)
}

func (pipe *scopePipeline) StrLen(ctx context.Context, k string) *redis.IntCmd {
	return pipe.client.StrLen(ctx, k)
}

func (c *scopeRedis) Pipelined(_ context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	c.roundTrips++
	if err := fn(&scopePipeline{client: c}); err != nil {
		return nil, err
	}
	return nil, c.getError
}

func (c *scopeRedis) HGet(ctx context.Context, k, field string) *redis.StringCmd {
	return redis.NewStringResult("", redis.Nil)
}

func (c *scopeRedis) MGet(ctx context.Context, keys ...string) *redis.SliceCmd {
	c.mgets++
	v := make([]interface{}, len(keys))
	for i, k := range keys {
		if x, ok := c.values[k]; ok {
			v[i] = x
		}
	}
	return redis.NewSliceResult(v, ctx.Err())
}
