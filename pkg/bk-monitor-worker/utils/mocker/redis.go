// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mocker

import (
	"context"
	"fmt"
	"strconv"

	mapset "github.com/deckarep/golang-set/v2"
	goRedis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
)

type RedisClientMocker struct {
	ZcountValue                  int64
	ZRangeByScoreWithScoresValue []goRedis.Z
	HMGetValue                   []any
	SetMap                       map[string]mapset.Set[string]
	HKeysValue                   []string
	goRedis.UniversalClient
}

func (r *RedisClientMocker) ZCount(ctx context.Context, key, min, max string) *goRedis.IntCmd {
	c := goRedis.NewIntCmd(ctx)
	c.SetVal(r.ZcountValue)
	return c
}

func (r *RedisClientMocker) ZRangeByScoreWithScores(ctx context.Context, key string, opt *goRedis.ZRangeBy) *goRedis.ZSliceCmd {
	c := goRedis.NewZSliceCmd(ctx)
	var filterRecords []goRedis.Z
	min, _ := strconv.ParseFloat(opt.Min, 64)
	max, _ := strconv.ParseFloat(opt.Max, 64)
	for _, z := range r.ZRangeByScoreWithScoresValue {
		if z.Score <= max && z.Score >= min {
			filterRecords = append(filterRecords, z)
		}
	}
	c.SetVal(filterRecords)
	return c
}

func (r *RedisClientMocker) HGet(ctx context.Context, key string, fields string) *goRedis.StringCmd {
	c := goRedis.NewStringCmd(ctx)
	c.SetVal("")
	return c
}

func (r *RedisClientMocker) HMGet(ctx context.Context, key string, fields ...string) *goRedis.SliceCmd {
	c := goRedis.NewSliceCmd(ctx)
	c.SetVal(r.HMGetValue)
	return c
}

func (r *RedisClientMocker) HSet(ctx context.Context, key string, values ...any) *goRedis.IntCmd {
	c := goRedis.NewIntCmd(ctx)
	s, ok := r.SetMap[key]
	if !ok {
		s = mapset.NewSet[string]()
		r.SetMap[key] = s
	}
	for _, v := range values {
		vStr, _ := v.(string)
		s.Add(vStr)
	}
	return c
}

func (r *RedisClientMocker) SAdd(ctx context.Context, key string, members ...any) *goRedis.IntCmd {
	c := goRedis.NewIntCmd(ctx)
	m, ok := r.SetMap[key]
	if !ok {
		m = mapset.NewSet[string]()
	}
	for _, member := range members {
		m.Add(member.(string))
	}
	r.SetMap[key] = m
	return c
}

func (r *RedisClientMocker) Publish(ctx context.Context, channel string, message any) *goRedis.IntCmd {
	return goRedis.NewIntCmd(ctx)
}

func (r *RedisClientMocker) Close() error {
	return nil
}

func (r *RedisClientMocker) HKeys(ctx context.Context, key string) *goRedis.StringSliceCmd {
	c := goRedis.NewStringSliceCmd(ctx)
	c.SetVal(r.HKeysValue)
	return c
}

func (r *RedisClientMocker) HDel(ctx context.Context, key string, fields ...string) *goRedis.IntCmd {
	c := goRedis.NewIntCmd(ctx)
	fmt.Println(fields)
	for _, key := range fields {
		r.HKeysValue = slicex.RemoveItem(r.HKeysValue, key)
	}
	return c
}
