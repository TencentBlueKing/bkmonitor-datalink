// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package window

import (
	"time"
)

type Runtime struct {
	// FirstExpiration expire time of first received
	FirstExpiration         time.Time
	Expiration              time.Time
	LastUpdateTime          time.Time
	ReentrantCount          int
	IncreaseExpirationCount int
}

// RuntimeConfig Different processing logic for different Windows needs to be implemented based on runtime.
type RuntimeConfig struct {
	MaxSize                 int
	ExpireInterval          time.Duration
	MaxDuration             time.Duration
	ExpireIntervalIncrement time.Duration
	NoDataMaxDuration       time.Duration
}

// ReentrantRuntimeStrategy Configure the runtime policy.
// You can customize different policies to change the window expiration time or to record additional information.
// All the different window implements has the Runtime instance.
type ReentrantRuntimeStrategy func(RuntimeConfig, *Runtime, CollectTrace)

var (
	ReentrantLogRecord ReentrantRuntimeStrategy = func(config RuntimeConfig, runtime *Runtime, _ CollectTrace) {
		newExpiration := runtime.Expiration.Add(config.ExpireIntervalIncrement)
		if newExpiration.Sub(runtime.FirstExpiration) >= config.MaxDuration {
			newExpiration = runtime.FirstExpiration.Add(config.MaxDuration)
		}
		runtime.Expiration = newExpiration
		runtime.IncreaseExpirationCount++
		runtime.ReentrantCount += 1
	}

	ReentrantLimitMaxCount ReentrantRuntimeStrategy = func(config RuntimeConfig, runtime *Runtime, collect CollectTrace) {
		if collect.Graph.Length() > config.MaxSize {
			runtime.Expiration = time.Now()
		}
	}

	RefreshUpdateTime ReentrantRuntimeStrategy = func(config RuntimeConfig, runtime *Runtime, v CollectTrace) {
		runtime.LastUpdateTime = time.Now()
	}

	PredicateLimitMaxDuration ReentrantRuntimeStrategy = func(config RuntimeConfig, runtime *Runtime, v CollectTrace) {
		if time.Since(runtime.FirstExpiration) > config.MaxDuration {
			runtime.Expiration = time.Now()
		}
	}

	PredicateNoDataDuration ReentrantRuntimeStrategy = func(config RuntimeConfig, runtime *Runtime, v CollectTrace) {
		if time.Since(runtime.LastUpdateTime) > config.NoDataMaxDuration {
			runtime.Expiration = time.Now()
		}
	}
)

type ConfigBaseRuntimeStrategies struct {
	config              RuntimeConfig
	reentrantStrategies []ReentrantRuntimeStrategy
	predicateStrategies []ReentrantRuntimeStrategy
}

func NewRuntimeStrategies(c RuntimeConfig, reentrantStrategies []ReentrantRuntimeStrategy, predicateStrategies []ReentrantRuntimeStrategy) *ConfigBaseRuntimeStrategies {
	return &ConfigBaseRuntimeStrategies{
		config:              c,
		reentrantStrategies: reentrantStrategies,
		predicateStrategies: predicateStrategies,
	}
}

func (c *ConfigBaseRuntimeStrategies) handleExist(runtime *Runtime, collect CollectTrace) {
	for _, strategy := range c.reentrantStrategies {
		strategy(c.config, runtime, collect)
	}
}

func (c *ConfigBaseRuntimeStrategies) handleNew() *Runtime {
	now := time.Now()
	return &Runtime{
		FirstExpiration:         now,
		Expiration:              now.Add(c.config.ExpireInterval),
		LastUpdateTime:          now,
		ReentrantCount:          0,
		IncreaseExpirationCount: 0,
	}
}

func (c *ConfigBaseRuntimeStrategies) predicate(runtime *Runtime, collect CollectTrace) bool {

	for _, strategy := range c.predicateStrategies {
		strategy(c.config, runtime, collect)
	}

	a := time.Now().After(runtime.Expiration)
	return a
}
