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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// capacityLoadSource publishes the profile the process derived for itself
// alongside what the container is actually consuming.
//
// The profile is already assembled for startup evidence and never changes
// afterwards, so it is captured once. Usage is read per scrape: two small
// cgroup files, on the same scrape that already walks every other collector.
func capacityLoadSource(facts observability.RuntimeConfigFacts) metric.CapacityLoadSource {
	// The keys are the budget label values capacity_transition_total reports
	// rejections under. They have to match exactly -- a ceiling that cannot be
	// joined to its rejections answers nothing.
	budgets := map[string]float64{
		"series":          float64(facts.Capacity.Series),
		"retained_bytes":  float64(facts.Capacity.RetainedBytes),
		"state_mutations": float64(facts.Capacity.StateMutations),
		"events":          float64(facts.Capacity.Events),
		"gap_mutations":   float64(facts.Capacity.GapMutations),
	}
	return func() metric.CapacityLoad {
		usage := config.ReadContainerUsage()
		return metric.CapacityLoad{
			Budgets:          budgets,
			MemoryLimitBytes: facts.MemoryLimitBytes,
			MemorySource:     facts.MemorySource,
			CPUSource:        facts.CPUSource,
			CPUCores:         facts.GOMAXPROCS,
			MemoryUsedBytes:  usage.MemoryBytes,
			MemoryUsedKnown:  usage.MemoryKnown,
			ThrottledSeconds: usage.ThrottledSeconds,
			ThrottledKnown:   usage.ThrottledKnown,
		}
	}
}
