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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

// flowFactsSource is the replica's stage counters as its summary carries
// them: the metrics' own values, copied at each publish, and the roles this
// process runs.
func flowFactsSource(recorder *metric.Recorder, running roles.Set) func() *fleet.FlowFacts {
	if recorder == nil {
		return nil
	}
	names := make([]string, 0, len(running))
	for _, role := range running {
		names = append(names, string(role))
	}
	return func() *fleet.FlowFacts {
		return flowFactsOf(recorder.FlowCounters(), names)
	}
}

func flowFactsOf(counters metric.FlowCounters, roleNames []string) *fleet.FlowFacts {
	facts := &fleet.FlowFacts{
		Queries: counters.Queries, QueryDuration: flowHistogram(counters.QueryDuration),
		Detections: counters.Detections, DecisionLag: flowHistogram(counters.DecisionLag),
		StateWrites: counters.StateWrites, StateFailures: counters.StateFailures, RuntimeRedis: flowHistogram(counters.RuntimeRedis),
		EventsEmitted: counters.EventsEmitted, EventsRejected: counters.EventsRejected,
		Hosts: counters.Hosts, Roles: append([]string(nil), roleNames...),
	}
	if len(counters.QueryFailures) > 0 {
		facts.QueryFailures = make(map[string]uint64, len(counters.QueryFailures))
		for cause, count := range counters.QueryFailures {
			facts.QueryFailures[cause] = count
		}
	}
	if counters.LiveHeapKnown {
		heap := counters.LiveHeapBytes
		facts.LiveHeapBytes = &heap
	}
	return facts
}

func flowHistogram(histogram metric.FlowHistogram) fleet.FlowHistogram {
	return fleet.FlowHistogram{Buckets: append([]uint64(nil), histogram.Buckets...), Count: histogram.Count}
}
