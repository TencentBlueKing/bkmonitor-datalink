// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"fmt"
	"sort"
	"strconv"
)

// The span export's states, closed. A process exports its own spans when it
// was given a collector (phase_two.tracing.otlp_endpoint); without one it
// exports nothing, and says so rather than going quiet: counters that stay
// at zero read the same for an export that is off and one with nothing to
// sample.
const (
	TracingOn               = "on"
	TracingOffNotConfigured = "off_not_configured"
	// TracingOffRole is a process without the worker role, which exports no
	// spans whatever the configuration says: the gauge's state there. A
	// replica's published facts never carry it - only a worker publishes.
	TracingOffRole = "off_role"
)

// TracingStates is every state, in the order the gauge creates them.
var TracingStates = []string{TracingOn, TracingOffNotConfigured, TracingOffRole}

// TracingReasonNotConfigured is why an export is off: no collector was given.
const TracingReasonNotConfigured = "not_configured"

// TracingFacts is the span export a process runs with, as it built it: on,
// with the collector's host and the sampling it was given, or off and why.
// It carries neither the endpoint's path nor the token, so it can be shown
// wherever the overview is.
type TracingFacts struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	// EndpointHost is the collector's host and port, nothing else of the URL.
	EndpointHost string `json:"endpoint_host,omitempty"`
	// SampleRatio and MaxTracesPerSecond are the sampling an export that is
	// on runs with, defaults resolved; absent when it is off. A ratio of 0 is
	// on and samples nothing, which is why they are pointers.
	SampleRatio        *float64 `json:"sample_ratio,omitempty"`
	MaxTracesPerSecond *int     `json:"max_traces_per_second,omitempty"`
}

// key is the facts as one comparable value: two replicas running the same
// export are one group.
func (facts TracingFacts) key() string {
	ratio, rate := "", ""
	if facts.SampleRatio != nil {
		ratio = strconv.FormatFloat(*facts.SampleRatio, 'g', -1, 64)
	}
	if facts.MaxTracesPerSecond != nil {
		rate = strconv.Itoa(*facts.MaxTracesPerSecond)
	}
	return facts.State + "\x00" + facts.Reason + "\x00" + facts.EndpointHost + "\x00" + ratio + "\x00" + rate
}

// CopyTracingFacts is the facts with their own pointers, or nil, so a later
// read cannot alias the source's: the publisher's facts into each snapshot,
// each snapshot's into the view.
func CopyTracingFacts(facts *TracingFacts) *TracingFacts {
	if facts == nil {
		return nil
	}
	copied := *facts
	if facts.SampleRatio != nil {
		ratio := *facts.SampleRatio
		copied.SampleRatio = &ratio
	}
	if facts.MaxTracesPerSecond != nil {
		rate := *facts.MaxTracesPerSecond
		copied.MaxTracesPerSecond = &rate
	}
	return &copied
}

// TracingGroup is one distinct span export and the counted replicas running
// it, grouped the way builds are: a deployment agreeing with itself shows one
// group, a rollout or a values file that changed under some replicas more. A
// replica that published none (a build before this fact) is its own group
// without facts, never folded into an export it may not be running.
type TracingGroup struct {
	Tracing  *TracingFacts `json:"tracing"`
	Replicas []string      `json:"replicas"`
}

// addToTracingGroup files a replica under the export it published.
func addToTracingGroup(groups []TracingGroup, facts *TracingFacts, replica string) []TracingGroup {
	key := "\x01none"
	if facts != nil {
		key = facts.key()
	}
	for index := range groups {
		existing := "\x01none"
		if groups[index].Tracing != nil {
			existing = groups[index].Tracing.key()
		}
		if existing == key {
			groups[index].Replicas = append(groups[index].Replicas, replica)
			return groups
		}
	}
	return append(groups, TracingGroup{Tracing: CopyTracingFacts(facts), Replicas: []string{replica}})
}

// sortTracingGroups puts the export most replicas run first, then orders by
// state and host so two reads of an evenly split deployment list the same way.
func sortTracingGroups(groups []TracingGroup) {
	sort.SliceStable(groups, func(i, j int) bool {
		if len(groups[i].Replicas) != len(groups[j].Replicas) {
			return len(groups[i].Replicas) > len(groups[j].Replicas)
		}
		left, right := "\x01none", "\x01none"
		if groups[i].Tracing != nil {
			left = groups[i].Tracing.key()
		}
		if groups[j].Tracing != nil {
			right = groups[j].Tracing.key()
		}
		return left < right
	})
}

// tracingList is the groups as an empty list rather than null: no counted
// replica is [] and is not the same statement as null.
func tracingList(groups []TracingGroup) []TracingGroup {
	if groups == nil {
		return []TracingGroup{}
	}
	return groups
}

// The overview's span export states: the replicas' one state when they
// agree, mixed when they do not, unknown when none published one.
const (
	OverviewTracingMixed   = "mixed"
	OverviewTracingUnknown = "unknown"
)

// OverviewTracing is the deployment's span export as the overview's one line:
// the state the replicas agree on, or mixed, or unknown; the facts when they
// agree; and the line the page shows.
type OverviewTracing struct {
	State  string        `json:"state"`
	Facts  *TracingFacts `json:"facts,omitempty"`
	Groups int           `json:"groups"`
	Line   string        `json:"line"`
}

// overviewTracingOf reads the replicas' export groups as the overview's line.
func overviewTracingOf(groups []TracingGroup) OverviewTracing {
	published := []TracingGroup{}
	for _, group := range groups {
		if group.Tracing != nil {
			published = append(published, group)
		}
	}
	switch {
	case len(published) == 0:
		return OverviewTracing{State: OverviewTracingUnknown, Groups: len(groups), Line: "调用链导出：没有副本报告（副本运行的版本早于这项事实）"}
	case len(groups) > 1:
		return OverviewTracing{State: OverviewTracingMixed, Groups: len(groups), Line: fmt.Sprintf("调用链导出：副本之间不一致（%d 种）", len(groups))}
	}
	facts := CopyTracingFacts(published[0].Tracing)
	out := OverviewTracing{State: facts.State, Facts: facts, Groups: 1}
	switch facts.State {
	case TracingOn:
		out.Line = fmt.Sprintf("调用链导出：开，发往 %s", facts.EndpointHost)
		if facts.SampleRatio != nil && facts.MaxTracesPerSecond != nil {
			out.Line += fmt.Sprintf("，采样 %s、每秒至多 %d 条", strconv.FormatFloat(*facts.SampleRatio, 'g', -1, 64), *facts.MaxTracesPerSecond)
		}
	case TracingOffNotConfigured:
		out.Line = "调用链导出：未配置（没有给接收端，alarmd 不导出调用链）"
	default:
		out.Line = "调用链导出：" + facts.State
	}
	return out
}
