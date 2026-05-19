// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pre_calculate

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/window"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/runtimex"
)

type dispatcher struct {
	ctx          context.Context
	dataId       string
	routes       map[core.AppKey]chan []window.StandardSpan
	singleTarget chan []window.StandardSpan
	errChan      chan<- error
}

func newDispatcher(
	ctx context.Context,
	dataId string,
	routes map[core.AppKey]chan []window.StandardSpan,
	errChan chan<- error,
) *dispatcher {
	var singleTarget chan []window.StandardSpan
	if len(routes) == 1 {
		for _, spanChan := range routes {
			singleTarget = spanChan
		}
	}

	return &dispatcher{
		ctx:          ctx,
		dataId:       dataId,
		routes:       routes,
		singleTarget: singleTarget,
		errChan:      errChan,
	}
}

func (d *dispatcher) Run(messageChan <-chan []window.StandardSpan) {
	defer runtimex.HandleCrashToChan(d.errChan)
	defer d.closeBundleSpanChans()

	buckets := make(map[chan []window.StandardSpan][]window.StandardSpan, len(d.routes))
	for _, spanChan := range d.routes {
		buckets[spanChan] = make([]window.StandardSpan, 0)
	}

	for {
		select {
		case batch, ok := <-messageChan:
			if !ok {
				apmLogger.Infof("[Dispatcher] messageChan closed, dataId: %s", d.dataId)
				return
			}
			d.dispatchBatch(batch, buckets)
		case <-d.ctx.Done():
			apmLogger.Infof("[Dispatcher] receive context done, dataId: %s", d.dataId)
			return
		}
	}
}

func (d *dispatcher) dispatchBatch(batch []window.StandardSpan, buckets map[chan []window.StandardSpan][]window.StandardSpan) {
	// buckets 仅作为单批次临时分桶，重置长度即可复用底层数组。
	for spanChan := range buckets {
		buckets[spanChan] = buckets[spanChan][:0]
	}

	for _, span := range batch {
		appKey := core.AppKey{BkBizId: span.BkBizId, AppName: span.AppName}
		if spanChan := d.route(appKey); spanChan != nil {
			buckets[spanChan] = append(buckets[spanChan], span)
		}
	}

	for spanChan, spans := range buckets {
		if len(spans) == 0 {
			continue
		}
		// 下发前复制一份，避免下一批复用 buckets 时覆盖下游仍在消费的数据。
		out := make([]window.StandardSpan, len(spans))
		copy(out, spans)
		select {
		case spanChan <- out:
		case <-d.ctx.Done():
			return
		}
	}
}

func (d *dispatcher) route(appKey core.AppKey) chan []window.StandardSpan {
	if appKey.IsZero() {
		return d.singleTarget
	}
	return d.routes[appKey]
}

func (d *dispatcher) closeBundleSpanChans() {
	for _, spanChan := range d.routes {
		close(spanChan)
	}
}
