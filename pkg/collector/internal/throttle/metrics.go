// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package throttle

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

var (
	droppedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "throttle_dropped_total",
			Help:      "Throttle dropped requests total",
		},
		[]string{"protocol", "record_type", "action"},
	)

	waterLevel = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "throttle_water_level",
			Help:      "Throttle resource water level",
		},
		[]string{"resource"},
	)

	throttleState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "throttle_state",
			Help:      "Throttle state by record type",
		},
		[]string{"record_type"},
	)
)

func IncDropped(protocol define.RequestType, recordType define.RecordType, action Action) {
	if action != ActionShed && action != ActionOpen {
		return
	}
	droppedTotal.WithLabelValues(protocol.S(), recordType.S(), action.S()).Inc()
}

func observeWaterLevel(level WaterLevel) {
	waterLevel.WithLabelValues("cpu_slow").Set(level.CPUSlow)
	waterLevel.WithLabelValues("cpu_fast").Set(level.CPUFast)
	if level.MemValid {
		waterLevel.WithLabelValues("mem").Set(level.Mem)
	}
}

func observeState(recordType define.RecordType, state State) {
	throttleState.WithLabelValues(recordType.S()).Set(float64(state))
}
