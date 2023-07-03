// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package durationmeasurer

import (
	"context"
	"sync"
	"time"

	"github.com/spenczar/tdigest"
)

// DurationMeasurer prometheus sdk 没有直接对 histogram 类型获取 p50/p90/p95 的 API 因此使用此类型来解决
type DurationMeasurer struct {
	ctx   context.Context
	mut   sync.Mutex
	tdg   tdigest.TDigest
	reset time.Duration
}

func New(ctx context.Context, reset time.Duration) *DurationMeasurer {
	dm := &DurationMeasurer{
		ctx:   ctx,
		tdg:   tdigest.New(),
		reset: reset,
	}
	go dm.cleanup()
	return dm
}

func (dm *DurationMeasurer) cleanup() {
	ticker := time.NewTicker(dm.reset)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			dm.mut.Lock()
			dm.tdg = tdigest.New()
			dm.mut.Unlock()

		case <-dm.ctx.Done():
			return
		}
	}
}

func (dm *DurationMeasurer) Measure(t time.Duration) {
	dm.mut.Lock()
	defer dm.mut.Unlock()
	dm.tdg.Add(t.Seconds(), 1)
}

func (dm *DurationMeasurer) P90() time.Duration {
	dm.mut.Lock()
	defer dm.mut.Unlock()
	return time.Duration(dm.tdg.Quantile(0.90) * 1e9)
}

func (dm *DurationMeasurer) P95() time.Duration {
	dm.mut.Lock()
	defer dm.mut.Unlock()
	return time.Duration(dm.tdg.Quantile(0.95) * 1e9)
}

func (dm *DurationMeasurer) P99() time.Duration {
	dm.mut.Lock()
	defer dm.mut.Unlock()
	return time.Duration(dm.tdg.Quantile(0.99) * 1e9)
}
