// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

// FlowFacts is a replica's own counters for the overview's four stages,
// copied at each publish from the metrics Prometheus scrapes, so a rate the
// overview takes over two of its computations and a chart over the scrape
// are one number. Fixed size whatever the replica holds: the summary
// carries it, bounded at FlowFactsMaxBytes.
type FlowFacts struct {
	// Queries are the physical queries run (worker_work_total
	// {work_kind="physical_query"}), QueryFailures those that did not
	// answer by cause (access_query_failure_total), QueryDuration
	// access_query_duration_seconds.
	Queries       uint64            `json:"queries"`
	QueryFailures map[string]uint64 `json:"query_failures,omitempty"`
	QueryDuration FlowHistogram     `json:"query_duration"`
	// Detections are the decided Slots (progress_completed_total less the
	// skipped and snapshot-unavailable kinds), DecisionLag
	// slot_decision_lag_seconds.
	Detections  uint64        `json:"detections"`
	DecisionLag FlowHistogram `json:"decision_lag"`
	// StateWrites are the applied state keys (worker_work_total
	// {work_kind="state_apply"}), StateFailures the admissions that did not
	// succeed and the version conflicts, RuntimeRedis the round trips of the
	// client serving runtime state (redis_command_duration_seconds under
	// client="runtime", or client="source" when it shares the source client).
	StateWrites   uint64        `json:"state_writes"`
	StateFailures uint64        `json:"state_failures"`
	RuntimeRedis  FlowHistogram `json:"runtime_redis"`
	// EventsEmitted are output_events_by_kind_total, EventsRejected
	// output_events_rejected_all_total.
	EventsEmitted  uint64 `json:"events_emitted"`
	EventsRejected uint64 `json:"events_rejected"`
	// LiveHeapBytes is the heap collector's last sample; absent before the
	// first.
	LiveHeapBytes *uint64 `json:"live_heap_bytes,omitempty"`
	// Hosts is the host index's size (cmdb_host_index_hosts).
	Hosts uint64 `json:"hosts,omitempty"`
	// Roles are the roles this process runs.
	Roles []string `json:"roles,omitempty"`
}

// FlowHistogram is a histogram's cumulative bucket counts in the order of
// its bounds (b) and its total (n); what lies past the last bound is Count
// less the last bucket. The keys are short because three of these are most
// of the block's bound.
type FlowHistogram struct {
	Buckets []uint64 `json:"b"`
	Count   uint64   `json:"n"`
}

// FlowFactsMaxBytes bounds the block a summary carries. With 37 bucket
// bounds and every counter at the test's ceiling of 10^12 the block encodes
// to 1076 bytes; 1152 leaves room for one more such field. It is a small part
// of a summary, which the snapshot budget bounds in MiB.
const FlowFactsMaxBytes = 1152

// The bounds of the three histograms, in seconds: the metrics' own, held
// equal to them by a test in the wiring.
var (
	FlowQueryBounds    = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30}
	FlowDecisionBounds = []float64{0.1, 0.5, 1, 2.5, 5, 10, 15, 20, 25, 30, 40, 50, 60, 120, 300}
	FlowRedisBounds    = []float64{0.0005, 0.001, 0.002, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5}
)
