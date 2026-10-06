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
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	httpservice "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/service/http"
)

const (
	livenessLoopControl  = "control"
	livenessLoopDispatch = "dispatch"
	// livenessExecutions names the stall of every execution slot at once.
	livenessExecutions = "executions"

	// The control loop's longest known turn is a cold refresh: the longest
	// interval read between two refreshes on a production deployment was
	// minutes, not tens of them. Fifteen minutes is an order of magnitude past
	// it. A kill here costs one restart; a miss costs a replica that never
	// detects again, so the bound sits on the wide side.
	controlLoopStallBound = 15 * time.Minute
	// A dispatch turn does queue work only, no I/O, so seconds are already
	// wrong; five minutes leaves room for a long GC pause or CPU throttling.
	dispatchLoopStallBound = 5 * time.Minute
	// An execution that honours its context returns at its deadline. One a
	// minute past it has ignored the context; when every slot holds one for
	// five minutes, nothing on this replica can detect and nothing but a
	// restart frees the slots.
	executionPastDeadlineGrace = time.Minute
	executionsStuckBound       = 5 * time.Minute
)

// phaseTwoLiveness is what the liveness probe judges: whether each started
// long-running loop still finishes turns, and whether the execution slots
// are all held by executions that ignored their deadline. A turn counts
// whether it succeeded or failed: a loop that keeps failing on a dependency
// is making progress, and restarting it would turn one outage into every
// replica restarting at once. Readiness reports that case; this does not.
type phaseTwoLiveness struct {
	now      func() time.Time
	recorder *metric.Recorder
	logger   *observability.Logger

	mu         sync.Mutex
	judging    bool
	loops      map[string]*livenessLoop
	slots      int
	executions map[uint64]time.Time
	nextToken  uint64
	lastReturn time.Time
	reported   map[string]bool
}

type livenessLoop struct {
	bound time.Duration
	last  time.Time
}

func newPhaseTwoLiveness(now func() time.Time, recorder *metric.Recorder) *phaseTwoLiveness {
	liveness := &phaseTwoLiveness{now: now, recorder: recorder, judging: true,
		loops: make(map[string]*livenessLoop), executions: make(map[uint64]time.Time), reported: make(map[string]bool)}
	recorder.SetLivenessSource(liveness.reading)
	return liveness
}

// start registers a loop from its first turn on. A loop is judged only once
// started, so a replica still waiting on a startup dependency is not late.
func (liveness *phaseTwoLiveness) start(loop string, bound time.Duration) {
	if liveness == nil {
		return
	}
	liveness.mu.Lock()
	defer liveness.mu.Unlock()
	liveness.loops[loop] = &livenessLoop{bound: bound, last: liveness.now()}
}

// turned records a finished turn of loop that began at began.
func (liveness *phaseTwoLiveness) turned(loop string, began time.Time) {
	if liveness == nil {
		return
	}
	now := liveness.now()
	liveness.mu.Lock()
	if state := liveness.loops[loop]; state != nil {
		state.last = now
	}
	liveness.mu.Unlock()
	liveness.recorder.ObserveLoopTurn(loop, now.Sub(began))
}

// clock is the time a turn begins at, read from the same clock it ends by.
func (liveness *phaseTwoLiveness) clock() time.Time {
	if liveness == nil {
		return time.Time{}
	}
	return liveness.now()
}

// stopJudging ends judgement for good: a draining replica's loops stop on
// purpose.
func (liveness *phaseTwoLiveness) stopJudging() {
	if liveness == nil {
		return
	}
	liveness.mu.Lock()
	liveness.judging = false
	liveness.mu.Unlock()
}

// setSlots is how many executions can run at once.
func (liveness *phaseTwoLiveness) setSlots(slots int) {
	if liveness == nil {
		return
	}
	liveness.mu.Lock()
	liveness.slots = slots
	liveness.mu.Unlock()
}

// executionStarted records an execution entering a slot under the deadline
// it was queued by; the zero deadline is one the probe cannot judge.
func (liveness *phaseTwoLiveness) executionStarted(deadline time.Time) uint64 {
	if liveness == nil {
		return 0
	}
	liveness.mu.Lock()
	defer liveness.mu.Unlock()
	liveness.nextToken++
	liveness.executions[liveness.nextToken] = deadline
	return liveness.nextToken
}

func (liveness *phaseTwoLiveness) executionReturned(token uint64) {
	if liveness == nil {
		return
	}
	liveness.mu.Lock()
	defer liveness.mu.Unlock()
	delete(liveness.executions, token)
	liveness.lastReturn = liveness.now()
}

// Stalls answers the liveness probe from memory.
func (liveness *phaseTwoLiveness) Stalls() []httpservice.Stall {
	if liveness == nil {
		return nil
	}
	now := liveness.now()
	liveness.mu.Lock()
	var stalls []httpservice.Stall
	if liveness.judging {
		for loop, state := range liveness.loops {
			if age := now.Sub(state.last); age > state.bound {
				stalls = append(stalls, httpservice.Stall{Loop: loop, Age: age, Bound: state.bound})
			}
		}
		if since, stuck := liveness.executionsStuckSinceLocked(); stuck {
			if age := now.Sub(since); age > executionsStuckBound {
				stalls = append(stalls, httpservice.Stall{Loop: livenessExecutions, Age: age, Bound: executionsStuckBound})
			}
		}
	}
	sort.Slice(stalls, func(i, j int) bool { return stalls[i].Loop < stalls[j].Loop })
	liveness.reportLocked(stalls)
	liveness.mu.Unlock()
	return stalls
}

// executionsStuckSinceLocked says whether every slot holds an execution past
// its deadline by the grace, and since when. The later of the last such
// crossing and the last return is the start: a return means a slot moved
// and the condition has not held across it.
func (liveness *phaseTwoLiveness) executionsStuckSinceLocked() (time.Time, bool) {
	if liveness.slots <= 0 || len(liveness.executions) < liveness.slots {
		return time.Time{}, false
	}
	var since time.Time
	for _, deadline := range liveness.executions {
		if deadline.IsZero() {
			return time.Time{}, false
		}
		if crossed := deadline.Add(executionPastDeadlineGrace); crossed.After(since) {
			since = crossed
		}
	}
	if liveness.lastReturn.After(since) {
		since = liveness.lastReturn
	}
	return since, !since.After(liveness.now())
}

// reportLocked logs a stall once when it starts and once when it ends.
func (liveness *phaseTwoLiveness) reportLocked(stalls []httpservice.Stall) {
	current := make(map[string]bool, len(stalls))
	for _, stall := range stalls {
		current[stall.Loop] = true
		if !liveness.reported[stall.Loop] && liveness.logger != nil {
			liveness.logger.Error("liveness", "stalled", 0, stall.Age,
				slog.String("loop", stall.Loop), slog.Duration("bound", stall.Bound))
		}
	}
	for loop := range liveness.reported {
		if !current[loop] && liveness.logger != nil {
			liveness.logger.Info("liveness", "resumed", 0, 0, slog.String("loop", loop))
		}
	}
	liveness.reported = current
}

// reading is the metric's view: each started loop's turn age, and how many
// executions are past their deadline by the grace.
func (liveness *phaseTwoLiveness) reading() metric.LivenessReading {
	now := liveness.now()
	liveness.mu.Lock()
	defer liveness.mu.Unlock()
	reading := metric.LivenessReading{TurnAge: make(map[string]time.Duration, len(liveness.loops))}
	for loop, state := range liveness.loops {
		reading.TurnAge[loop] = now.Sub(state.last)
	}
	for _, deadline := range liveness.executions {
		if !deadline.IsZero() && now.After(deadline.Add(executionPastDeadlineGrace)) {
			reading.ExecutionsPastDeadline++
		}
	}
	return reading
}
