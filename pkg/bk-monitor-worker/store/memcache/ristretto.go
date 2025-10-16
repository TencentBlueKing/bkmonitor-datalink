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
	"sync"
	"time"

	"github.com/dgraph-io/ristretto"
)

const (
	RistrettoNumCounters        = 1e7
	RistrettoMaxCost            = 1 << 8
	RistrettoBufferItems        = 64
	RistrettoIgnoreInternalCost = true
)

type Ristretto struct {
	cache *ristretto.Cache
}

var memCache *Ristretto

var once sync.Once

// GetMemCache get memory cache
func GetMemCache() (*Ristretto, error) {
	if memCache != nil {
		return memCache, nil
	}
	var err error
	once.Do(func() {
		memCache, err = NewRistretto()
	})
	return memCache, err
}

// NewRistretto new a memory cache
func NewRistretto() (*Ristretto, error) {
	c, err := ristretto.NewCache(&ristretto.Config{
		NumCounters:        RistrettoNumCounters,
		MaxCost:            RistrettoMaxCost,
		BufferItems:        RistrettoBufferItems,
		IgnoreInternalCost: RistrettoIgnoreInternalCost,
	})
	if err != nil {
		return nil, fmt.Errorf("new ristretto cache error, %v", err)
	}
	return &Ristretto{cache: c}, nil
}

// Get get key
func (c *Ristretto) Get(key string) (any, bool) {
	return c.cache.Get(key)
}

// Put set a new key-val
func (c *Ristretto) Put(key string, val any, cost int64) bool {
	return c.cache.Set(key, val, cost)
}

// PutWithTTL set a new key-val with ttl
func (c *Ristretto) PutWithTTL(key string, val any, cost int64, t time.Duration) bool {
	return c.cache.SetWithTTL(key, val, cost, t)
}

// Delete delete a key
func (c *Ristretto) Delete(key string) {
	c.cache.Del(key)
}

// Wait for value to pass through buffers
func (c *Ristretto) Wait() {
	c.cache.Wait()
}

func (c *Ristretto) Close() {
	c.cache.Close()
}
