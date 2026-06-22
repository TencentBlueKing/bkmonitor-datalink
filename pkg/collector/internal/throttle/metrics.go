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
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

var (
	metricsOnce   sync.Once
	requestsTotal *prometheus.CounterVec
	waterLevel    *prometheus.GaugeVec
	throttleState *prometheus.GaugeVec
)

func initMetrics() {
	metricsOnce.Do(func() {
		requestsTotal = promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: define.MonitoringNamespace,
				Name:      "throttle_requests_total",
				Help:      "Throttle requests total",
			},
			[]string{"protocol", "record_type", "decision"},
		)

		waterLevel = promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: define.MonitoringNamespace,
				Name:      "throttle_water_level",
				Help:      "Throttle resource water level",
			},
			[]string{"kind"},
		)

		throttleState = promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: define.MonitoringNamespace,
				Name:      "throttle_state",
				Help:      "Throttle state by record type",
			},
			[]string{"record_type"},
		)
	})
}

const (
	decisionAllowed = "allowed"
	decisionDenied  = "denied"
)

func IncRequest(protocol define.RequestType, recordType define.RecordType, action Action) {
	initMetrics()

	decision := decisionAllowed
	if action != ActionAdmit {
		decision = decisionDenied
	}
	requestsTotal.WithLabelValues(protocol.S(), recordType.S(), decision).Inc()
}

func observeWaterLevel(level WaterLevel, thresholds ThresholdConfig) {
	initMetrics()

	waterLevel.WithLabelValues("cpu").Set(level.CPU)
	waterLevel.WithLabelValues("cpu_slow").Set(level.CPUSlow)
	waterLevel.WithLabelValues("cpu_fast").Set(level.CPUFast)
	if level.MemValid {
		waterLevel.WithLabelValues("mem").Set(level.Mem)
	}
	waterLevel.WithLabelValues("cpu_enter").Set(thresholds.CPUEnter)
	waterLevel.WithLabelValues("cpu_exit").Set(thresholds.CPUExit)
	waterLevel.WithLabelValues("cpu_hard").Set(thresholds.CPUHard)
	waterLevel.WithLabelValues("mem_enter").Set(thresholds.MemEnter)
	waterLevel.WithLabelValues("mem_exit").Set(thresholds.MemExit)
	waterLevel.WithLabelValues("mem_hard").Set(thresholds.MemHard)
}

func observeState(recordType define.RecordType, state State) {
	initMetrics()

	throttleState.WithLabelValues(recordType.S()).Set(float64(state))
}
