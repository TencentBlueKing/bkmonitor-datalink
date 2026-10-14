// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"time"

	"linkd/internal/controlplane/taskstate"
	"linkd/internal/telemetry"
)

// deliveryTaskObservation 将实际循环通知并列写入当前任务状态和通用指标；业务专属指标由类型观察器保留。
// 每个固定阶段串行报告页面，阶段之间并发；目录创建不会使 Activity 活跃。
type deliveryTaskObservation struct {
	registry *taskstate.Registry
	metrics  *telemetry.Runtime
	activity map[string]*taskstate.Activity
}

func newDeliveryTaskObservation(registry *taskstate.Registry, metrics *telemetry.Runtime, phases ...string) (*deliveryTaskObservation, error) {
	o := &deliveryTaskObservation{registry: registry, metrics: metrics, activity: map[string]*taskstate.Activity{}}
	for _, id := range phases {
		activity, err := registry.ObserveActivity(id)
		if err != nil {
			return nil, err
		}
		o.activity[id] = activity
	}
	return o, nil
}

func (o *deliveryTaskObservation) setRunning(ctx context.Context, id string, active bool) {
	if a := o.activity[id]; a != nil {
		a.SetActive(active)
		o.metrics.ControlPlaneTaskObserver(telemetry.ControlPlaneTask(id)).SetActive(ctx, active)
	}
}

func (o *deliveryTaskObservation) started(id string) {
	if o.activity[id] != nil {
		o.registry.Begin(id, "", "")
	}
}

func (o *deliveryTaskObservation) finished(ctx context.Context, id string, duration time.Duration, scanned, failed int, code string) {
	if o.activity[id] == nil {
		return
	}
	switch code {
	case "", "scan_failed", "invalid_page", "item_failed", "cancelled", "round_timeout":
	default:
		code = "invalid_result"
	}
	if scanned < 0 || failed < 0 || duration < 0 {
		scanned, failed, duration, code = 0, 1, 0, "invalid_result"
	}
	if code != "" && failed == 0 {
		failed = 1
	}
	o.registry.Finish(ctx, id, "", "", duration, scanned, failed, code)
	if ctx.Err() == nil {
		o.metrics.ControlPlaneTaskObserver(telemetry.ControlPlaneTask(id)).RunFinished(ctx, duration, code == "" && failed == 0)
	}
}
