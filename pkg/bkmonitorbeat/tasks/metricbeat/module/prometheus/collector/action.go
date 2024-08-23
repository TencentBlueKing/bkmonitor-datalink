// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import "sync"

const (
	ActionTypeDelta = "delta"
	ActionTypeRate  = "rate"
)

type ActionConfigs struct {
	Rate  []ActionRate
	Delta ActionDelta
}

// ActionRate 扩展的 rate action
// 支持对指标计算 rate 进行并生成新指标
type ActionRate struct {
	Source      string // 原指标
	Destination string // 新指标
}

// ActionDelta 扩展的 delta action
// 支持对指标进行 delta 计算 并且覆盖原指标 value
type ActionDelta []string

type actionOperator struct {
	action string
	mut    sync.Mutex

	rateKeys  map[string]string
	deltaKeys map[string]struct{}

	values     map[string]map[string]float64
	timestamps map[string]map[string]int64
}

func newActionOperator(action string, rateOps []ActionRate, deltaOps ActionDelta) *actionOperator {
	rateKeys := make(map[string]string)
	for _, op := range rateOps {
		rateKeys[op.Source] = op.Destination
	}
	deltaKeys := make(map[string]struct{})
	for _, op := range deltaOps {
		deltaKeys[op] = struct{}{}
	}

	return &actionOperator{
		action:     action,
		rateKeys:   rateKeys,
		deltaKeys:  deltaKeys,
		values:     make(map[string]map[string]float64),
		timestamps: make(map[string]map[string]int64),
	}
}

// GetOrUpdate 获取或者更新数值
// 如果 bool 为 true 则表示可以直接使用该 value 结果
func (ao *actionOperator) GetOrUpdate(metric, h string, ts int64, value float64) (string, float64, bool) {
	if ao.action == ActionTypeDelta {
		newV, ok := ao.getOrUpdateDelta(metric, h, value)
		return metric, newV, ok
	}

	// ActionTypeRate(default)
	return ao.getOrUpdateRate(metric, h, ts, value)
}

func (ao *actionOperator) getOrUpdateDelta(metric, h string, value float64) (float64, bool) {
	ao.mut.Lock()
	defer ao.mut.Unlock()

	if _, ok := ao.deltaKeys[metric]; !ok {
		return value, true
	}

	if _, ok := ao.values[metric]; !ok {
		ao.values[metric] = make(map[string]float64)
	}

	v, ok := ao.values[metric][h]
	if ok {
		deltaV := value - v
		ao.values[metric][h] = value
		return deltaV, true
	}

	ao.values[metric][h] = value
	return 0, false
}

func (ao *actionOperator) getOrUpdateRate(metric, h string, ts int64, value float64) (string, float64, bool) {
	ao.mut.Lock()
	defer ao.mut.Unlock()

	newMetric, ok := ao.rateKeys[metric]
	if !ok {
		return metric, value, true
	}

	if _, ok := ao.values[metric]; !ok {
		ao.values[metric] = make(map[string]float64)
	}
	if _, ok := ao.timestamps[metric]; !ok {
		ao.timestamps[metric] = make(map[string]int64)
	}

	// values/timestamp 一定同时更新
	v, ok := ao.values[metric][h]
	if ok {
		deltaV := value - v
		deltaT := ts - ao.timestamps[metric][h]
		r := (deltaV) / float64(deltaT)

		ao.values[metric][h] = value
		ao.timestamps[metric][h] = ts
		return newMetric, r, true
	}

	ao.values[metric][h] = value
	ao.timestamps[metric][h] = ts
	return newMetric, 0, false
}
