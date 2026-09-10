// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// queryPermitOccupancySource adapts the scheduler's occupancy to the metric
// package's shape. The translation lives here rather than in either package so
// neither has to depend on the other for the sake of a metric.
func queryPermitOccupancySource(flights *scheduler.FlightCoordinator) metric.QueryPermitOccupancySource {
	return func() metric.QueryPermitOccupancy {
		occupancy := flights.QueryPermitOccupancy()
		translated := metric.QueryPermitOccupancy{
			Inflight: make(map[string]int, len(occupancy.Inflight)),
			// Waiting is already keyed by queue name and carries no operation
			// identity, so it passes through unchanged.
			Waiting:        occupancy.Waiting,
			HeldSeconds:    make(map[string]float64, len(occupancy.HeldSeconds)),
			Budget:         occupancy.Budget,
			RecoveryBudget: occupancy.RecoveryBudget,
		}
		for operation, count := range occupancy.Inflight {
			translated.Inflight[string(operation)] = count
		}
		for operation, seconds := range occupancy.HeldSeconds {
			translated.HeldSeconds[string(operation)] = seconds
		}
		return translated
	}
}
