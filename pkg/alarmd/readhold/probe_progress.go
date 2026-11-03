// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package readhold

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A shadow probe reads ProbationRounds rounds clean at least - two hours at a
// minute's period, eight near a floor a probe failed at, forty at twenty
// minutes - and a rollout or a takeover in between used to start it over:
// with the trials' candidate kept in the record (VerifyCandidateMillis), a
// probe that never finishes keeps its group probing for good. So the record
// keeps how far the probe has read (Record.Probe), and the next process goes
// on from there.
//
// The progress is written every ProbeProgressEvery clean rounds: one write a
// group's ten rounds, beside the record writes its rounds make anyway. It is
// read-modify-fenced - the controller's current record with the progress
// alone changed, compared and set against the bytes it read - and a write
// that loses its compare is dropped, not tried again: whatever won moved the
// record, and a move of what the probe reads is no progress of it. A probe's
// end and every write that moves what it reads clear the progress in the
// same write (FloorProbeResult, persist), so no process goes on from rounds
// read before a round that read differently.
const ProbeProgressEvery = 10

// What came of a probe's progress written, closed: written; stale, the
// record's probe no longer the one the progress is of - its end, or a move
// of what it reads, taken first - so nothing is written; conflict, the
// compare lost to a write by another, so the progress is dropped.
const (
	ProbeProgressWritten  = "written"
	ProbeProgressStale    = "stale"
	ProbeProgressConflict = "conflict"
)

// ProbeProgressOutcomes is every way a probe's progress write ends.
var ProbeProgressOutcomes = []string{ProbeProgressWritten, ProbeProgressStale, ProbeProgressConflict}

// FloorProbeProgressEvidence is how far a running probe has read: the hold
// its rounds are read at (Floor) and its Kind, the hold of their formal reads,
// its clean rounds in a row, the Slot it started at and the distance between
// its rounds (Period). Contract is the Slot of its latest clean round.
type FloorProbeProgressEvidence struct {
	Contract execution.FrozenExecutionContractRef
	Floor    time.Duration
	Kind     string
	Hold     time.Duration
	Clean    int
	Started  execution.EvaluationTime
	Period   time.Duration
}

// progressStale is whether kept rounds whose latest was read at kept are too
// old to go on from at now: FloorStanding, or, for a probe whose rounds are
// so far apart that ProbeProgressEvery of them take longer, the time from
// one write to the next and a round more - so a probe taken over just before
// its next write still goes on.
func progressStale(probe ProbeProgress, now int64) bool {
	standing := max(FloorStanding.Milliseconds(), int64(ProbeProgressEvery+1)*probe.PeriodMillis)
	return now-int64(probe.KeptSlot)*1000 >= standing
}

// progressOf is the record's progress of the probe of kind at candidate, and
// false when the record keeps none for it: none kept, or one of another
// probe - another kind, holds or floor - or one whose latest kept round is
// stale (progressStale), whose rounds say nothing of the data now.
func progressOf(record Record, kind string, candidate, now int64) (ProbeProgress, bool) {
	probe := record.Probe
	if probe == nil || probe.Kind != kind || probe.CandidateMillis != candidate || probe.HoldMillis != current(record) {
		return ProbeProgress{}, false
	}
	if (probe.FloorMillis == nil) != (record.EarlierFloorMillis == nil) ||
		(probe.FloorMillis != nil && *probe.FloorMillis != *record.EarlierFloorMillis) {
		return ProbeProgress{}, false
	}
	if progressStale(*probe, now) {
		return ProbeProgress{}, false
	}
	return *probe, true
}

// settleProgress clears from a record about to be written a progress that is
// not of the probe the record has now (probeCandidate): the write moved what
// the probe reads, or ends it.
func settleProgress(qg execution.QueryGroupIdentity, record *Record, step time.Duration, now int64) {
	if record.Probe == nil {
		return
	}
	candidate, kind, _, ok := probeCandidate(qg, *record, step, now)
	if _, kept := progressOf(*record, kind, candidate, now); !ok || !kept {
		record.Probe = nil
	}
}

// FloorProbeResume is how far the group's probe of candidate, of kind, under
// the group's hold now had read when the process running it stopped running
// it - its clean rounds in a row and the Slot it started at - and false when
// the record keeps none for that probe (progressOf).
func (controller *Controller) FloorProbeResume(qg execution.QueryGroupIdentity, kind string, candidate time.Duration) (int, execution.EvaluationTime, bool) {
	state := controller.group(qg)
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.loaded {
		return 0, 0, false
	}
	progress, ok := progressOf(state.record, kind, candidate.Milliseconds(), controller.options.Now().UnixMilli())
	return progress.Clean, progress.StartedSlot, ok
}

// FloorProbeProgress keeps how far a running probe has read, in the record:
// only while the record's probe is the one the evidence is of, and only once,
// the write dropped when it loses its compare.
func (controller *Controller) FloorProbeProgress(ctx context.Context, evidence FloorProbeProgressEvidence) error {
	if evidence.Contract.Validate() != nil || evidence.Floor < 0 || evidence.Hold <= evidence.Floor ||
		!slices.Contains(FloorProbeKinds, evidence.Kind) || evidence.Clean < 0 || evidence.Started <= 0 || evidence.Period <= 0 ||
		evidence.Contract.Slot.EvaluationTime < evidence.Started {
		return errors.New("alarmd readhold: invalid floor probe progress")
	}
	qg := evidence.Contract.Slot.QueryGroup
	state := controller.group(qg)
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := ready(state); err != nil {
		return err
	}
	if evidence.Contract.ScheduleSegmentStart < state.record.SegmentStart || state.record.Closed {
		return nil
	}
	now := controller.options.Now().UnixMilli()
	candidate, kind, _, ok := probeCandidate(qg, state.record, state.spec.Step, now)
	if !ok || kind != evidence.Kind || candidate != evidence.Floor.Milliseconds() || current(state.record) != evidence.Hold.Milliseconds() {
		controller.counted(controller.probeProgress, ProbeProgressStale)
		return nil
	}
	next := clone(state.record)
	next.Probe = &ProbeProgress{Kind: kind, CandidateMillis: candidate, HoldMillis: current(next), Clean: evidence.Clean,
		StartedSlot: evidence.Started, KeptSlot: evidence.Contract.Slot.EvaluationTime, PeriodMillis: evidence.Period.Milliseconds()}
	if next.EarlierFloorMillis != nil {
		floor := *next.EarlierFloorMillis
		next.Probe.FloorMillis = &floor
	}
	switch err := controller.persist(ctx, qg, state, next, nil); {
	case errors.Is(err, ErrConflict):
		controller.counted(controller.probeProgress, ProbeProgressConflict)
		return nil
	case err != nil:
		return err
	}
	controller.counted(controller.probeProgress, ProbeProgressWritten)
	return nil
}
