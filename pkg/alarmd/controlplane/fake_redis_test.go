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
