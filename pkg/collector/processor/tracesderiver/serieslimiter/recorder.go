// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package serieslimiter

import (
	"sync/atomic"
	"time"
)

// recorder 提供的是 bloomfilter 算法的计算方式 即不能保证 100% 的 hash 准确性
// 因为 series 数量是个比较粗粒度的数字 这里牺牲准确性换取性能
type recorder struct {
	dataID   int32
	maxItems int
	v        atomic.Value
	done     chan struct{}
}

func newRecorder(dataID int32, maxItems int, refreshInterval time.Duration) *recorder {
	l := &recorder{
		dataID:   dataID,
		maxItems: maxItems,
		done:     make(chan struct{}, 1),
	}
	l.v.Store(newBloomLimiter(dataID, maxItems))

	go l.updateStore(refreshInterval)
	go l.updateMetrics()
	return l
}

func (l *recorder) updateStore(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-l.done:
			return
		case <-ticker.C:
			l.v.Store(newBloomLimiter(l.dataID, l.maxItems))
		}
	}
}

// updateMetrics 定期更新指标
func (l *recorder) updateMetrics() {
	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()

	for {
		select {
		case <-l.done:
			return
		case <-ticker.C:
			DefaultMetricMonitor.SetSeriesCount(l.dataID, l.CurrentItems())
		}
	}
}

func (l *recorder) CurrentItems() int {
	lm := l.v.Load().(*bloomFilter)
	n := atomic.LoadUint64(&lm.currentItems)
	return int(n)
}

func (l *recorder) Set(h uint64) bool {
	lm := l.v.Load().(*bloomFilter)
	return lm.Set(h)
}

func (l *recorder) Stop() {
	close(l.done)
}

// Bloom Filter

type bloomFilter struct {
	dataID       int32
	currentItems uint64
	f            *filter
}

func newBloomLimiter(dataID int32, maxItems int) *bloomFilter {
	return &bloomFilter{
		dataID: dataID,
		f:      newFilter(maxItems),
	}
}

// Set 返回 true 如果 series 已经存在或者被成功添加
func (l *bloomFilter) Set(h uint64) bool {
	currentItems := atomic.LoadUint64(&l.currentItems)
	if currentItems >= uint64(l.f.maxItems) {
		has := l.f.Has(h)
		if !has {
			DefaultMetricMonitor.IncSeriesExceededCounter(l.dataID)
		}
		return has
	}
	if l.f.Add(h) {
		atomic.AddUint64(&l.currentItems, 1)
		DefaultMetricMonitor.IncAddedSeriesCounter(l.dataID)
	}
	return true
}
