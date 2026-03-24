// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package memcache

import (
	"fmt"
	"time"

	"github.com/dgraph-io/ristretto"
	"github.com/spf13/viper"
)

const (
	RistrettoNumCountersPath          = "memcache.ristretto.num_counters"
	RistrettoMaxCostPath              = "memcache.ristretto.max_cost"
	RistrettoBufferItemsPath          = "memcache.ristretto.buffer_items"
	RistrettoIgnoreInternalCostPath   = "memcache.ristretto.ignore_internal_cost"
	RistrettoExpiredTimePath          = "memcache.ristretto.expired_time"
	RistrettoExpiredTimeFluxValuePath = "memcache.ristretto.expired_time_flux_value"
)

func init() {
	viper.SetDefault(RistrettoNumCountersPath, 1e7)
	viper.SetDefault(RistrettoMaxCostPath, 1<<30)
	viper.SetDefault(RistrettoBufferItemsPath, 64)
	viper.SetDefault(RistrettoIgnoreInternalCostPath, true)
	viper.SetDefault(RistrettoExpiredTimePath, 60)          // 过期时间，单位 min
	viper.SetDefault(RistrettoExpiredTimeFluxValuePath, 20) // 过期时间，单位 min
}

type Ristretto struct {
	cache *ristretto.Cache
}

func NewRistretto() (*Ristretto, error) {
	c, err := ristretto.NewCache(&ristretto.Config{
		NumCounters:        viper.GetInt64(RistrettoNumCountersPath),
		MaxCost:            viper.GetInt64(RistrettoMaxCostPath),
		BufferItems:        viper.GetInt64(RistrettoBufferItemsPath),
		IgnoreInternalCost: viper.GetBool(RistrettoIgnoreInternalCostPath),
	})
	if err != nil {
		return nil, fmt.Errorf("new ristretto cache error, %v", err)
	}
	return &Ristretto{cache: c}, nil
}

func (c *Ristretto) Get(key string) (any, bool) {
	return c.cache.Get(key)
}

func (c *Ristretto) Set(key string, val any, cost int64) bool {
	return c.cache.Set(key, val, cost)
}

func (c *Ristretto) SetWithTTL(key string, val any, cost int64, t time.Duration) bool {
	return c.cache.SetWithTTL(key, val, cost, t)
}

func (c *Ristretto) Del(key string) {
	c.cache.Del(key)
}

func (c *Ristretto) Clear() {
	c.cache.Clear()
}
