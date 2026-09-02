// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"errors"
	"math"
	"sort"
	"sync"
	"time"
)

type Resource string
type ResourceState string

const (
	ResourceCPU              Resource = "cpu"
	ResourceRSS              Resource = "rss"
	ResourceHeap             Resource = "heap"
	ResourceGC               Resource = "gc"
	ResourceWorkerQueueDepth Resource = "worker_queue_depth"
	ResourceWorkerQueueBytes Resource = "worker_queue_bytes"
	ResourceInflightMessages Resource = "inflight_messages"
	ResourceInflightBytes    Resource = "inflight_bytes"
	ResourceConsumerLag      Resource = "consumer_lag"
	ResourceStateBytes       Resource = "state_bytes"

	ResourceNormal ResourceState = "normal"
	ResourceSoft   ResourceState = "soft"
	ResourceHard   ResourceState = "hard"
	ResourceResume ResourceState = "resume"
)

type ResourceSnapshot struct {
	ObservedAt         time.Time
	CPUCores           float64
	RSSBytes           float64
	HeapBytes          float64
	GCPauseSeconds     float64
	WorkerQueueDepth   float64
	WorkerQueueBytes   float64
	InflightMessages   float64
	InflightBytes      float64
	ConsumerLagRecords float64
	StateBytes         float64
}

type ResourceSignal struct {
	State      ResourceState
	Reasons    []ReasonCode
	ObservedAt time.Time
}

type ResourceThreshold struct {
	Resume float64
	Soft   float64
	Hard   float64
}

type ResourceGovernorConfig struct {
	Thresholds     map[Resource]ResourceThreshold
	SustainSamples int
	ResumeSamples  int
}

type ResourceGovernor interface {
	Observe(ResourceSnapshot) ResourceSignal
}

type ResourceSource interface {
	ResourceSnapshot() ResourceSnapshot
	ResourceSignal() ResourceSignal
}

// ObservingResourceGovernor only computes and exposes ResourceSignal. It does
// not change concurrency, pause consumption, or execute any governance action.
type ObservingResourceGovernor struct {
	mu sync.RWMutex

	thresholds     map[Resource]ResourceThreshold
	sustainSamples int
	resumeSamples  int
	state          ResourceState
	reasons        []ReasonCode
	candidate      ResourceState
	candidateCount int
	latest         ResourceSnapshot
	signal         ResourceSignal
}

func NewResourceGovernor(config ResourceGovernorConfig) (*ObservingResourceGovernor, error) {
	thresholds := make(map[Resource]ResourceThreshold, len(config.Thresholds))
	for resource, threshold := range config.Thresholds {
		if !resourceCanGate(resource) {
			return nil, errors.New("observability: resource is observation-only or unsupported")
		}
		if threshold.Resume < 0 || threshold.Soft <= 0 || threshold.Hard <= 0 ||
			threshold.Resume > threshold.Soft || threshold.Soft > threshold.Hard {
			return nil, errors.New("observability: resource threshold must satisfy resume <= soft <= hard")
		}
		thresholds[resource] = threshold
	}
	if config.SustainSamples <= 0 {
		config.SustainSamples = 1
	}
	if config.ResumeSamples <= 0 {
		config.ResumeSamples = 1
	}
	return &ObservingResourceGovernor{
		thresholds:     thresholds,
		sustainSamples: config.SustainSamples,
		resumeSamples:  config.ResumeSamples,
		state:          ResourceNormal,
		signal:         ResourceSignal{State: ResourceNormal},
	}, nil
}

func (g *ObservingResourceGovernor) Observe(snapshot ResourceSnapshot) ResourceSignal {
	if g == nil {
		return ResourceSignal{State: ResourceNormal, ObservedAt: snapshot.ObservedAt}
	}
	if snapshot.ObservedAt.IsZero() {
		snapshot.ObservedAt = time.Now()
	}
	snapshot = NormalizeResourceSnapshot(snapshot)

	g.mu.Lock()
	defer g.mu.Unlock()
	g.latest = snapshot
	desired, reasons, belowResume := g.classify(snapshot)
	g.advance(desired, reasons, belowResume)
	g.signal = ResourceSignal{
		State:      g.state,
		Reasons:    append([]ReasonCode(nil), g.reasons...),
		ObservedAt: snapshot.ObservedAt,
	}
	return g.copySignalLocked()
}

func (g *ObservingResourceGovernor) ResourceSnapshot() ResourceSnapshot {
	if g == nil {
		return ResourceSnapshot{}
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.latest
}

func (g *ObservingResourceGovernor) ResourceSignal() ResourceSignal {
	if g == nil {
		return ResourceSignal{State: ResourceNormal}
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.copySignalLocked()
}

func NormalizeResourceSignal(signal ResourceSignal) ResourceSignal {
	signal.State = normalizeResourceState(signal.State)
	signal.Reasons = NormalizeMetricReasons(ComponentResource, signal.Reasons)
	return signal
}

func (g *ObservingResourceGovernor) copySignalLocked() ResourceSignal {
	signal := g.signal
	signal.Reasons = append([]ReasonCode(nil), g.signal.Reasons...)
	return signal
}

func (g *ObservingResourceGovernor) classify(snapshot ResourceSnapshot) (ResourceState, []ReasonCode, bool) {
	softReasons := make([]ReasonCode, 0, len(g.thresholds))
	hardReasons := make([]ReasonCode, 0, len(g.thresholds))
	belowResume := true
	for resource, threshold := range g.thresholds {
		value := resourceValue(snapshot, resource)
		if value < 0 {
			belowResume = false
			continue
		}
		if value > threshold.Resume {
			belowResume = false
		}
		switch {
		case value >= threshold.Hard:
			hardReasons = append(hardReasons, reasonForResource(resource))
		case value >= threshold.Soft:
			softReasons = append(softReasons, reasonForResource(resource))
		}
	}
	state := ResourceNormal
	reasons := softReasons
	if len(hardReasons) > 0 {
		state = ResourceHard
		reasons = hardReasons
	} else if len(softReasons) > 0 {
		state = ResourceSoft
	}
	sort.Slice(reasons, func(i, j int) bool { return reasons[i] < reasons[j] })
	return state, reasons, belowResume
}

func (g *ObservingResourceGovernor) advance(desired ResourceState, reasons []ReasonCode, belowResume bool) {
	switch g.state {
	case ResourceNormal:
		if desired == ResourceSoft || desired == ResourceHard {
			g.transitionAfter(desired, reasons, g.sustainSamples)
		} else {
			g.resetCandidate()
		}
	case ResourceSoft:
		switch {
		case desired == ResourceHard:
			g.transitionAfter(ResourceHard, reasons, g.sustainSamples)
		case belowResume:
			g.transitionAfter(ResourceResume, g.reasons, g.resumeSamples)
		default:
			if len(reasons) > 0 {
				g.reasons = append([]ReasonCode(nil), reasons...)
			}
			g.resetCandidate()
		}
	case ResourceHard:
		if belowResume {
			g.transitionAfter(ResourceResume, g.reasons, g.resumeSamples)
		} else {
			g.resetCandidate()
		}
	case ResourceResume:
		switch desired {
		case ResourceHard, ResourceSoft:
			g.transitionAfter(desired, reasons, g.sustainSamples)
		default:
			g.state = ResourceNormal
			g.reasons = nil
			g.resetCandidate()
		}
	default:
		g.state = ResourceNormal
		g.reasons = nil
		g.resetCandidate()
	}
}

func (g *ObservingResourceGovernor) transitionAfter(state ResourceState, reasons []ReasonCode, required int) {
	if g.candidate != state {
		g.candidate = state
		g.candidateCount = 0
	}
	g.candidateCount++
	if g.candidateCount < required {
		return
	}
	g.state = state
	g.reasons = append([]ReasonCode(nil), reasons...)
	g.resetCandidate()
}

func (g *ObservingResourceGovernor) resetCandidate() {
	g.candidate = ""
	g.candidateCount = 0
}

func NormalizeResourceSnapshot(snapshot ResourceSnapshot) ResourceSnapshot {
	values := []*float64{
		&snapshot.CPUCores, &snapshot.RSSBytes, &snapshot.HeapBytes, &snapshot.GCPauseSeconds,
		&snapshot.WorkerQueueDepth, &snapshot.WorkerQueueBytes, &snapshot.InflightMessages,
		&snapshot.InflightBytes, &snapshot.ConsumerLagRecords, &snapshot.StateBytes,
	}
	for _, value := range values {
		if *value < 0 || math.IsNaN(*value) || math.IsInf(*value, 0) {
			*value = -1
		}
	}
	return snapshot
}

func normalizeResourceState(state ResourceState) ResourceState {
	switch state {
	case ResourceSoft, ResourceHard, ResourceResume:
		return state
	case ResourceNormal, "":
		return ResourceNormal
	default:
		return ResourceNormal
	}
}

func AllResourceStates() []ResourceState {
	return []ResourceState{ResourceNormal, ResourceSoft, ResourceHard, ResourceResume}
}

func resourceCanGate(resource Resource) bool {
	switch resource {
	case ResourceCPU, ResourceRSS, ResourceHeap, ResourceGC, ResourceWorkerQueueDepth,
		ResourceWorkerQueueBytes, ResourceInflightMessages, ResourceInflightBytes:
		return true
	default:
		return false
	}
}

func resourceValue(snapshot ResourceSnapshot, resource Resource) float64 {
	switch resource {
	case ResourceCPU:
		return snapshot.CPUCores
	case ResourceRSS:
		return snapshot.RSSBytes
	case ResourceHeap:
		return snapshot.HeapBytes
	case ResourceGC:
		return snapshot.GCPauseSeconds
	case ResourceWorkerQueueDepth:
		return snapshot.WorkerQueueDepth
	case ResourceWorkerQueueBytes:
		return snapshot.WorkerQueueBytes
	case ResourceInflightMessages:
		return snapshot.InflightMessages
	case ResourceInflightBytes:
		return snapshot.InflightBytes
	default:
		return -1
	}
}

func reasonForResource(resource Resource) ReasonCode {
	switch resource {
	case ResourceCPU:
		return ReasonCPU
	case ResourceRSS:
		return ReasonRSS
	case ResourceHeap:
		return ReasonHeap
	case ResourceGC:
		return ReasonGC
	case ResourceWorkerQueueDepth, ResourceWorkerQueueBytes:
		return ReasonWorkerQueue
	case ResourceInflightMessages, ResourceInflightBytes:
		return ReasonInflight
	case ResourceConsumerLag:
		return ReasonConsumerLag
	case ResourceStateBytes:
		return ReasonStateBytes
	default:
		return ReasonOther
	}
}
