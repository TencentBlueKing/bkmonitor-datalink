// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import "time"

// The capacity panel reported CPU, memory and permits and left "is it
// enough" to the reader, and a reader with two thousand objects where there
// were nine hundred could not answer it from those. What an operator needs
// to know is whether the work is finishing on time, whether the backlog is
// growing, whether detection is being lost, and which resource -- if any --
// the evidence points at; the resource numbers are the evidence, not the
// answer. This is that judgment, decided here from the same view the page
// and the metrics read, each reading a closed state with the numbers it was
// read from and the limits it was read under. It does not say how many more
// objects the deployment could carry: nothing here measures that, and a
// number invented for it would be the one a reader acted on.

// Load is the operating judgment: four readings and the limits on them.
type Load struct {
	OnTime     LoadOnTime     `json:"on_time"`
	Backlog    LoadBacklog    `json:"backlog"`
	Loss       LoadLoss       `json:"loss"`
	Bottleneck LoadBottleneck `json:"bottleneck"`
	// Limits are the conditions the readings hold under, as closed keys the
	// page has words for. They travel with the judgment so a reader who
	// takes the conclusion takes its bounds with it.
	Limits []LoadLimit `json:"limits"`
}

// OnTimeState is whether the work is finishing before its next turn.
type OnTimeState string

const (
	// OnTimeKeepingUp: nothing overdue.
	OnTimeKeepingUp OnTimeState = "KEEPING_UP"
	// OnTimeCatchingUp: objects overdue, but the last hour's on-time rate is
	// not below the last six hours' -- the deployment is not losing ground.
	OnTimeCatchingUp OnTimeState = "CATCHING_UP"
	// OnTimeFallingBehind: objects overdue and the short window worse than
	// the long one, the SRE workbook's two-window reading of a burn.
	OnTimeFallingBehind OnTimeState = "FALLING_BEHIND"
	// OnTimeUnknown: no replica has a due index, so nothing can say.
	OnTimeUnknown OnTimeState = "UNKNOWN"
)

// LoadOnTime is the on-time reading with the census numbers it came from.
type LoadOnTime struct {
	State   OnTimeState `json:"state"`
	Overdue int         `json:"overdue"`
	// OldestLateSeconds is how far past due the furthest-behind object is.
	OldestLateSeconds float64 `json:"oldest_late_seconds"`
	// Rate1h and Rate6h are on-time rates in percent; absent when the window
	// had no completed rounds, which is not the same as zero percent.
	Rate1h *float64 `json:"rate_1h,omitempty"`
	Rate6h *float64 `json:"rate_6h,omitempty"`
}

// BacklogState is whether the overdue count is growing.
type BacklogState string

const (
	BacklogNone      BacklogState = "NONE"
	BacklogGrowing   BacklogState = "GROWING"
	BacklogShrinking BacklogState = "SHRINKING"
	BacklogFlat      BacklogState = "FLAT"
	// BacklogUnknown: no earlier sample to compare against, or one too young
	// to be a trend.
	BacklogUnknown BacklogState = "UNKNOWN"
)

// LoadBacklog is the backlog reading: the overdue count now and the count
// the index found SpanSeconds ago.
type LoadBacklog struct {
	State       BacklogState `json:"state"`
	Now         int          `json:"now"`
	Earlier     *int         `json:"earlier,omitempty"`
	SpanSeconds float64      `json:"span_seconds,omitempty"`
}

// LossState is whether detection is being lost now.
type LossState string

const (
	LossNone       LossState = "NONE"
	LossInProgress LossState = "IN_PROGRESS"
)

// LoadLoss is the loss reading: objects losing rounds now; the ones whose
// loss is their replica's restart catching up, apart because it stops on
// its own; and the demoted objects that skipped within the window -- the
// refusal's consequence, apart because no capacity changes it.
type LoadLoss struct {
	State        LossState `json:"state"`
	Ongoing      int       `json:"ongoing"`
	AfterRestart int       `json:"after_restart"`
	// AfterCooldown is the recent records a cooldown held on an object that
	// has since left the pool: with the restart's, a loss that names its
	// mechanism and asks no capacity question. In progress all the same.
	AfterCooldown       int `json:"after_cooldown"`
	WhileDemotedRecent  int `json:"while_demoted_recent"`
	WindowSeconds       int `json:"window_seconds"`
	RestartGraceSeconds int `json:"restart_grace_seconds"`
}

// Bottleneck is the resource the evidence points at, or that it points at
// none.
type Bottleneck string

const (
	// BottleneckNone: the work is on time and nothing is being lost; no
	// resource is asked about.
	BottleneckNone Bottleneck = "NONE"
	// BottleneckBudget: the memory-derived budgets rejected work. The one
	// reading that is capacity by construction: the resources do not fit the
	// load.
	BottleneckBudget Bottleneck = "BUDGET"
	// BottleneckMemory: the container reached its memory limit.
	BottleneckMemory Bottleneck = "MEMORY"
	// BottleneckCPU: the container was throttled for a large share of its
	// CPU time.
	BottleneckCPU Bottleneck = "CPU"
	// BottleneckPermits: most permit acquisitions queued, while the work is
	// behind or being lost -- the query concurrency is the constraint.
	BottleneckPermits Bottleneck = "PERMITS"
	// BottleneckQueue: the ready queue turned objects away, while the work is
	// behind or being lost.
	BottleneckQueue Bottleneck = "QUEUE"
	// BottleneckSkew: the work is behind or being lost while the scheduler's
	// own round says the ready replicas hold uneven shares. The constraint
	// is the split, not a resource: the loaded replica's permits and queue
	// are full because it holds what the others do not, and adding replicas
	// or resources does not move an object off it. Read before the permit
	// and queue readings, which on the loaded replica are this seen from
	// the resource side.
	BottleneckSkew Bottleneck = "SKEW"
	// BottleneckUnlocated: the work is behind or being lost and no resource
	// reading points anywhere -- the constraint is the schedule (a replay
	// bound, a dispatch order), not a resource, and adding resources is not
	// the move.
	BottleneckUnlocated Bottleneck = "UNLOCATED"
	// BottleneckUnknown: no capacity facts to read.
	BottleneckUnknown Bottleneck = "UNKNOWN"
)

// LoadBottleneck is the bottleneck reading with the resource numbers it
// was read from. The shares are since process start.
type LoadBottleneck struct {
	Resource         Bottleneck `json:"resource"`
	BudgetRejections uint64     `json:"budget_rejections"`
	MemoryLimitHits  uint64     `json:"memory_limit_hits"`
	// ThrottledShare is throttled seconds over CPU seconds, absent when
	// throttling is not measured.
	ThrottledShare *float64 `json:"throttled_share,omitempty"`
	// PermitWaitShare is permit waits over acquires, absent before any
	// acquisition.
	PermitWaitShare *float64 `json:"permit_wait_share,omitempty"`
	QueueFull       uint64   `json:"queue_full"`
	// Skew is the leader's planning round when it would move objects, on
	// every reading and not only the one named SKEW: a budget or memory
	// reading taken while one replica holds nearly everything is that
	// replica's, and the sentence has to say so.
	Skew *RebalanceFacts `json:"skew,omitempty"`
}

// LoadLimit is one condition a reading holds under.
type LoadLimit string

const (
	// LimitCountersSinceStart: the resource shares are cumulative since each
	// process started, not the last hour.
	LimitCountersSinceStart LoadLimit = "COUNTERS_SINCE_START"
	// LimitTrendSpan: the backlog trend spans less than an hour, because the
	// youngest replica's index is younger than that.
	LimitTrendSpan LoadLimit = "TREND_SPAN_SHORT"
	// LimitNoCensus: no replica has a due index.
	LimitNoCensus LoadLimit = "NO_CENSUS"
	// LimitNoCapacity: no replica reported capacity facts.
	LimitNoCapacity LoadLimit = "NO_CAPACITY"
	// LimitNoHeadroomEstimate: nothing here says how many more objects the
	// deployment could carry, and nothing is invented for it.
	LimitNoHeadroomEstimate LoadLimit = "NO_HEADROOM_ESTIMATE"
)

// Thresholds the readings are decided on. Constants, not settings: an
// operator is not better placed than the program to pick them, and a knob
// here is a second meaning for the number.
const (
	// backlogTrendMinSpan is the shortest span a backlog comparison is read
	// as a trend over.
	backlogTrendMinSpan = 10 * time.Minute
	// backlogTrendMargin is how many more (or fewer) overdue objects count as
	// growth (or shrinkage) rather than noise: two, or a tenth, whichever is
	// larger.
	backlogTrendMargin      = 2
	backlogTrendMarginShare = 0.1
	// throttledShareBottleneck is the throttled share of CPU time past which
	// CPU is named.
	throttledShareBottleneck = 0.2
	// permitWaitShareBottleneck is the share of permit acquisitions that
	// queued past which the permits are named, when the work is behind.
	permitWaitShareBottleneck = 0.5
)

// LoadOf decides the judgment from the view.
func LoadOf(view *View, now time.Time) Load {
	load := Load{Limits: []LoadLimit{LimitNoHeadroomEstimate}}
	load.OnTime, load.Backlog = onTimeOf(view.Schedule), backlogOf(view.Schedule)
	load.Loss = lossOfView(view, now)
	// The restart's catch-up asks no capacity question: only a loss by some
	// other mechanism does.
	behind := load.OnTime.State == OnTimeFallingBehind || load.OnTime.State == OnTimeCatchingUp ||
		load.Backlog.State == BacklogGrowing || load.Loss.Ongoing > 0
	load.Bottleneck = bottleneckOf(view.Capacity, behind, view.Rebalance)
	if view.Schedule == nil {
		load.Limits = append(load.Limits, LimitNoCensus)
	} else if load.Backlog.Earlier != nil && load.Backlog.SpanSeconds < 3600 {
		load.Limits = append(load.Limits, LimitTrendSpan)
	}
	if view.Capacity == nil {
		load.Limits = append(load.Limits, LimitNoCapacity)
	} else {
		load.Limits = append(load.Limits, LimitCountersSinceStart)
	}
	return load
}

func rateOf(onTime, completed int) *float64 {
	if completed == 0 {
		return nil
	}
	rate := 100 * float64(onTime) / float64(completed)
	return &rate
}

// onTimeOf is the two-window reading: behind when something is overdue and
// the short window is worse than the long one. The same rule the page's
// first sentence and its queue verdict read.
func onTimeOf(census *ScheduleCensus) LoadOnTime {
	if census == nil {
		return LoadOnTime{State: OnTimeUnknown}
	}
	reading := LoadOnTime{Overdue: census.Overdue, OldestLateSeconds: census.OldestLateSeconds,
		Rate1h: rateOf(census.OnTime1h, census.Completed1h), Rate6h: rateOf(census.OnTime6h, census.Completed6h)}
	switch {
	case census.Overdue == 0:
		reading.State = OnTimeKeepingUp
	case reading.Rate1h != nil && reading.Rate6h != nil && *reading.Rate1h < *reading.Rate6h:
		reading.State = OnTimeFallingBehind
	default:
		reading.State = OnTimeCatchingUp
	}
	return reading
}

// backlogOf compares the overdue count with the one the index found
// earlier. A comparison over less than backlogTrendMinSpan is not a trend
// and reads UNKNOWN; within the margin it reads FLAT.
func backlogOf(census *ScheduleCensus) LoadBacklog {
	if census == nil {
		return LoadBacklog{State: BacklogUnknown}
	}
	reading := LoadBacklog{Now: census.Overdue, Earlier: census.OverdueAgo, SpanSeconds: census.OverdueAgoSeconds}
	switch {
	case census.OverdueAgo == nil || census.OverdueAgoSeconds < backlogTrendMinSpan.Seconds():
		reading.State = BacklogUnknown
	default:
		earlier := *census.OverdueAgo
		margin := backlogTrendMargin
		if share := int(float64(earlier) * backlogTrendMarginShare); share > margin {
			margin = share
		}
		switch {
		case census.Overdue == 0 && earlier == 0:
			reading.State = BacklogNone
		case census.Overdue-earlier > margin:
			reading.State = BacklogGrowing
		case earlier-census.Overdue > margin:
			reading.State = BacklogShrinking
		default:
			reading.State = BacklogFlat
		}
	}
	return reading
}

// lossOfView counts the records the way the first screen does: in progress
// when not demoted and within the window; the demoted objects' recent
// skips apart.
func lossOfView(view *View, now time.Time) LoadLoss {
	reading := LoadLoss{State: LossNone, WindowSeconds: int(RecentSkipWindow / time.Second),
		RestartGraceSeconds: int(RestartCatchUpGrace / time.Second)}
	lossRecords(view, now, func(_ string, _, _ Check, _ string, skip SkippedSpan, loss Loss, _ bool) {
		switch loss {
		case LossOngoing:
			reading.Ongoing++
		case LossAfterRestart:
			reading.AfterRestart++
		case LossAfterCooldown:
			reading.AfterCooldown++
		case LossWhileDemoted:
			if now.Sub(skip.At) <= RecentSkipWindow {
				reading.WhileDemotedRecent++
			}
		}
	})
	if reading.Ongoing > 0 || reading.AfterRestart > 0 || reading.AfterCooldown > 0 {
		reading.State = LossInProgress
	}
	return reading
}

// bottleneckOf names the resource the evidence points at. Most specific
// evidence first: a budget rejection is capacity by construction and is
// named whether or not the work is behind; a memory limit hit and heavy
// throttling likewise; queued permits and a full queue are named only when
// the work is behind or being lost, because on a deployment keeping up
// they are what a full-enough deployment looks like, not a constraint.
// Behind with no resource pointing anywhere is its own answer.
func bottleneckOf(capacity *CapacityView, behind bool, rebalance *RebalanceFacts) LoadBottleneck {
	var skew *RebalanceFacts
	if rebalance.Skewed() {
		facts := *rebalance
		skew = &facts
	}
	if capacity == nil {
		return LoadBottleneck{Resource: BottleneckUnknown, Skew: skew}
	}
	reading := LoadBottleneck{MemoryLimitHits: capacity.MemoryLimitHits, Skew: skew}
	for _, count := range capacity.Rejections {
		reading.BudgetRejections += count
	}
	if capacity.Rotation != nil {
		reading.QueueFull = capacity.Rotation.DeferredQueueFull
	}
	if capacity.ThrottledKnown && capacity.CPUSeconds > 0 {
		share := capacity.ThrottledSeconds / capacity.CPUSeconds
		reading.ThrottledShare = &share
	}
	if capacity.PermitAcquires > 0 {
		share := float64(capacity.PermitWaits) / float64(capacity.PermitAcquires)
		reading.PermitWaitShare = &share
	}
	switch {
	case reading.BudgetRejections > 0:
		reading.Resource = BottleneckBudget
	case reading.MemoryLimitHits > 0 || capacity.MemoryOOMKills > 0:
		reading.Resource = BottleneckMemory
	case reading.ThrottledShare != nil && *reading.ThrottledShare >= throttledShareBottleneck:
		reading.Resource = BottleneckCPU
	case !behind:
		reading.Resource = BottleneckNone
	case skew != nil:
		reading.Resource = BottleneckSkew
	case reading.PermitWaitShare != nil && *reading.PermitWaitShare >= permitWaitShareBottleneck:
		reading.Resource = BottleneckPermits
	case reading.QueueFull > 0:
		reading.Resource = BottleneckQueue
	default:
		reading.Resource = BottleneckUnlocated
	}
	return reading
}

// Closed lists, for the page's completeness tests.
var (
	OnTimeStates  = []OnTimeState{OnTimeKeepingUp, OnTimeCatchingUp, OnTimeFallingBehind, OnTimeUnknown}
	BacklogStates = []BacklogState{BacklogNone, BacklogGrowing, BacklogShrinking, BacklogFlat, BacklogUnknown}
	LossStates    = []LossState{LossNone, LossInProgress}
	Bottlenecks   = []Bottleneck{BottleneckNone, BottleneckBudget, BottleneckMemory, BottleneckCPU, BottleneckSkew, BottleneckPermits, BottleneckQueue, BottleneckUnlocated, BottleneckUnknown}
	LoadLimits    = []LoadLimit{LimitCountersSinceStart, LimitTrendSpan, LimitNoCensus, LimitNoCapacity, LimitNoHeadroomEstimate}
)
