// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
)

// A no-data Plan's round over its memory read in parts (decision-020 batch 8,
// 2.7, plan 5.1-4 and 5.1-8), after every present window has landed:
//
//  1. The present set: every series of the Plan projected onto its group,
//     kept as the windows landed (dropWindowInputs) and the rest added now,
//     one key and a few bits a group (nodata.PresentSet). A series a retry
//     resumed is a prepared series like any other and is in it.
//  2. The header was read with the Slot's no-data load (ReadNoDataHeaders);
//     the groups are scanned a segment at a time (HSCAN COUNT): a present
//     group is fed to the round at once, setting its seen bit; any other
//     name goes into the absent-name set, deduplicated by the digest of its
//     name, which is also what sets aside a field the scan returns twice.
//     The segment's values go as it is judged.
//  3. The header is read again. One that moved is a record another writer
//     changed during the scan, which is not this Slot's memory to judge: the
//     Slot fails as a retryable read (NoDataRosterMovedError) and is retried.
//  4. The absent names, sorted by digest, are read back by name a window at a
//     time (HMGET), fed, and the series they say are synthesized and
//     evaluated like any other: through the same batches and the same
//     windows, output acknowledged before State.
//  5. The round finishes: the present groups never stored are new groups in
//     the delta, the Plan-level facts are settled, the groups nothing stored
//     (an expected group never seen, the whole item) and the present ones are
//     said, and the statement is built from the delta and the fold. It is
//     written once, in the tail, as every Slot writes its memory: a delta
//     against the header read at the start, or the staged whole write when
//     the round read no record.
//
// What the round holds across this, by structure, against the share: the
// present set, the absent-name set, the delta, and while it is read one
// segment or one absence window.

// noDataSegmentGroups is how many fields one scan of a memory asks for and
// how many names one absence window reads.
func (coordinator *SlotExecutionCoordinator) noDataSegmentGroups() int {
	if coordinator.noDataSegment > 0 {
		return coordinator.noDataSegment
	}
	return execution.NoDataRosterSegmentGroups
}

// What the round's structures hold, by entry. A map entry of a string key is
// counted at its header and its value with the map's control byte, filled to
// seven eighths, rounded up to a word.
const (
	// presentKeyEntryBytes is a present key's map entry beside its text: a
	// string header and a flag byte.
	presentKeyEntryBytes = 32
	// absentNameEntryBytes is an absent name's entry beside its text: the
	// name's digest, a string header in the map and again in the sorted list.
	absentNameEntryBytes = sha256.Size + 16 + 8 + sha256.Size + 16
	// absenceStartEntryBytes is an absent series' entry in the Slot's
	// absence starts (noDataAbsenceStarts): its 64-character digest, a string
	// header and the time.
	absenceStartEntryBytes = 64 + 16 + 8 + 8
)

var scannedGroupBytes = uint64(unsafe.Sizeof(execution.NoDataGroupMemory{}))

func presentKeyHeldBytes(key string) uint64 { return presentKeyEntryBytes + uint64(len(key)) }

// groupsHeldBytes is what a segment or an absence window holds while it is
// read: each group and its key, and the store's field as it came back.
func groupsHeldBytes(groups []execution.NoDataGroupMemory) uint64 {
	var total uint64
	for _, group := range groups {
		total += scannedGroupBytes + 2*uint64(len(group.GroupKey)) + noDataAbsentValueBytes
	}
	return total
}

// NoDataRosterMovedError is a no-data memory whose header moved while the Slot
// read it in parts, or whose group read back by name was not there: another
// writer changed the record, so what was read is no one record. The Slot is
// retried, as for any read that failed and may succeed again.
type NoDataRosterMovedError struct {
	Plan   execution.PlanNoDataIdentity
	Reason string
}

func (err *NoDataRosterMovedError) Error() string {
	return fmt.Sprintf("alarmd worker: the no-data memory of strategy %s moved while it was read: %s",
		err.Plan.Plan.StrategyID, err.Reason)
}

// NoDataRosterReadReason is the word a Slot failed on its no-data memory's
// parts names: a record that moved under the read is a write the Slot did not
// make landing on it (STATE_WRITE_RETRYABLE, the word a staged write that
// lost its key names), and a part the store could not answer is the store
// unavailable.
func NoDataRosterReadReason(err error) (string, bool) {
	var moved *NoDataRosterMovedError
	if errors.As(err, &moved) {
		return contract.ReasonStateWriteRetryable, true
	}
	var unread *noDataRosterUnreadError
	if errors.As(err, &unread) {
		return string(unread.reason), true
	}
	return "", false
}

// noDataRosterUnreadError is a part of the memory the store could not read.
type noDataRosterUnreadError struct {
	plan   execution.PlanNoDataIdentity
	reason execution.ReasonCode
	err    error
}

func (err *noDataRosterUnreadError) Error() string {
	return fmt.Sprintf("alarmd worker: no-data memory of strategy %s was not read (%s): %v",
		err.plan.Plan.StrategyID, err.reason, err.err)
}

func (err *noDataRosterUnreadError) Unwrap() error { return err.err }

func noDataRosterUnread(plan execution.PlanNoDataIdentity, reason execution.ReasonCode, err error) error {
	if reason == "" {
		reason = execution.ReasonCode(contract.ReasonRedisUnavailable)
	}
	return &noDataRosterUnreadError{plan: plan, reason: reason, err: err}
}

// reserveNoDataHeld reserves what a no-data round holds, by structure, under
// one phase and against the share (reserveRetained). A coordinator with no
// retained budget - only a test builds one - holds nothing, as
// reserveNoDataMemory has it.
func (stream *streamedExecution) reserveNoDataHeld(ctx context.Context, phase retainPhase, bytes uint64) error {
	if stream.coordinator.budget.MaxRetainedBytes == 0 {
		return nil
	}
	return stream.reserveRetained(ctx, phase, noDataReservationPhase, bytes)
}

// presentSetFor is the Plan's present set with every prepared series of the
// Plan still held added to the ones its landed windows kept.
func (stream *streamedExecution) presentSetFor(
	ctx context.Context, due execution.DuePlan, prepared []preparedSeries,
) (*nodata.PresentSet, error) {
	set := stream.windows.presentSet(due)
	var added uint64
	for _, dimensions := range stream.seriesDimensionsFor(prepared, due.Identity) {
		if key, fresh := set.Add(dimensions); fresh {
			added += presentKeyHeldBytes(key)
		}
	}
	if err := stream.reserveNoDataHeld(ctx, retainPhaseInput, added); err != nil {
		return nil, err
	}
	return set, nil
}

// presentSet is the set the Plan's landed windows keep, made on first use.
func (windows *slotWindows) presentSet(due execution.DuePlan) *nodata.PresentSet {
	if set := windows.present[due.Identity]; set != nil {
		return set
	}
	if windows.present == nil {
		windows.present = make(map[execution.PlanIdentity]*nodata.PresentSet)
	}
	set := nodata.NewPresentSet(due.CompiledPlan.NoData().AggDimension)
	windows.present[due.Identity] = set
	return set
}

// absentName is a name the scan found outside the present set.
type absentName struct {
	digest [sha256.Size]byte
	name   string
}

// segmentedNoDataRound is one Plan's round while it is being read.
type segmentedNoDataRound struct {
	stream   *streamedExecution
	due      execution.DuePlan
	identity execution.PlanNoDataIdentity
	// source is the memory read: the Plan's own, or the generation its
	// activation carries it from; header is that memory's header as the
	// Slot's load read it.
	source  execution.PlanNoDataIdentity
	header  execution.NoDataMemorySnapshot
	scanned bool
	round   *nodata.SegmentedRound
	view    execution.DuePlan
	version execution.ApplyVersion
	// history and hosts resolve a history roster's hosts as its groups are
	// read (nodata.HistoryHostCandidate).
	history bool
	hosts   execution.HostBusiness
	absent  map[[sha256.Size]byte]string
	// extra is what the round holds beside its delta - the absent names and
	// the absence starts - and charged what it has reserved for both,
	// against the share.
	extra, charged uint64
	// names is the absent-name set's part of extra, given back once the
	// absence windows are read.
	names   uint64
	emit    func(completedSeries) error
	emitted int
	starts  map[string]int64
}

// decideNoDataRound reads, judges and says one Plan's round, handing each
// series it says to emit as it goes, and returns the round with its statement.
func (stream *streamedExecution) decideNoDataRound(
	ctx context.Context, due execution.DuePlan, present *nodata.PresentSet, completeness execution.Completeness,
	emit func(completedSeries) error,
) (noDataRound, error) {
	defer stream.enter(slotPhaseNoDataRead)()
	config := due.CompiledPlan.NoData()
	if config == nil {
		return noDataRound{outcome: nodata.OutcomeNone}, nil
	}
	identity := due.NoDataIdentity()
	snapshot, found := stream.noData.Find(identity)
	if !found {
		return noDataRound{}, derivationFailed(fmt.Errorf(
			"alarmd worker: no-data memory for strategy %s was not loaded", due.Identity.StrategyID))
	}
	version, err := execution.BuildApplyVersion(stream.header.Contract, due.StateApplyEpoch)
	if err != nil {
		return noDataRound{}, derivationFailed(err)
	}
	period := int64(due.CompiledPlan.EvaluationSemantics().EvaluationInterval)
	if err := noDataPointGrid(int64(stream.header.Contract.Slot.EvaluationTime), period); err != nil {
		return noDataRound{}, derivationFailed(err)
	}
	hosts := stream.noDataHosts[identity]
	round, err := nodata.NewSegmentedRound(nodata.PlanSlotInput{
		NoData:           config,
		Scope:            due.CompiledPlan.TargetScope(),
		Plan:             due.CompiledPlan.TargetPlan(),
		TargetResolution: stream.targetResolutions[identity.Plan].absenceView(),
		AnswerTruncated:  stream.answerTruncated[due.Identity],
		Identity:         identity,
		Snapshot:         snapshot,
		ApplyVersion:     version,
		ScheduleRevision: due.ScheduleRevision,
		EvaluationTime:   int64(stream.header.Contract.Slot.EvaluationTime),
		PeriodSeconds:    period,
		Completeness:     completeness,
		Present:          present,
		KnownHosts:       hosts.Known,
		HostsResolved:    hosts.Resolved,
		OutOfBusiness:    hosts.OutOfBusiness,
		// Forwarded from the Plan, as compilation froze it: see the whole
		// round's comment in noDataRoundFor's history.
		TrackingHorizonSeconds: config.TrackingHorizonSeconds,
		MaxGroups:              config.MaxGroups,
		ManualCloses:           manualClosesFor(due, snapshot.ManualCloses),
	})
	if err != nil {
		return noDataRound{}, derivationFailed(err)
	}
	result := noDataRound{outcome: round.Outcome(), horizon: config.TrackingHorizonSeconds,
		horizonSource: string(config.TrackingHorizonSource)}
	view, err := execution.PlanViewFor(due, execution.SeriesKindNoData)
	if err != nil {
		return noDataRound{}, outputFailed(err)
	}
	reading := &segmentedNoDataRound{stream: stream, due: due, identity: identity, source: identity, header: snapshot,
		round: round, view: view, version: version, emit: emit}
	if carried, carriedFrom := stream.noDataCarried[identity]; carriedFrom {
		reading.source, reading.header = carried.Identity, carried
	}
	reading.scanned = reading.header.Status == execution.NoDataMemoryFound
	if class, classErr := nodata.ClassifyTarget(due.CompiledPlan.TargetScope(), due.CompiledPlan.TargetPlan(),
		config.AggDimension); classErr == nil && class.Source == nodata.RosterHistory && hosts.Resolved {
		reading.history = true
		reading.hosts = execution.HostBusinessFor(stream.coordinator.ports.Hosts, due.Identity.TenantID)
	}
	if round.Judges() {
		if err := reading.scan(ctx); err != nil {
			return noDataRound{}, err
		}
		// The absences are counted now and said after every prepared
		// series: the watchdog's ceiling gets their windows.
		stream.raiseWindowCeiling(len(reading.absent))
		round.Settle()
		if err := reading.readAbsent(ctx); err != nil {
			return noDataRound{}, err
		}
	}
	// What the windows staged of the whole record ahead of the statement.
	round.UseStaged(stream.stagedAhead(due.Identity))
	finished, err := round.Finish(version)
	if err != nil {
		return noDataRound{}, derivationFailed(err)
	}
	stream.endStagedAhead(ctx, due, finished)
	if err := reading.hold(ctx, 0); err != nil {
		return noDataRound{}, err
	}
	result.outcome, result.facts, result.mutation = finished.Outcome, finished.Facts, finished.Mutation
	if !finished.Judged {
		return result, nil
	}
	// What Finish has the round say - the groups no stored entry named and
	// the present groups' NORMAL - are series too, said after every prepared
	// one and not in S either: a present group's real series was counted, its
	// synthetic one was not. The ceiling gets their windows as it got the
	// absences'.
	stream.raiseWindowCeiling(len(finished.Rest) + len(finished.PresentKeys))
	if err := reading.say(ctx, finished.Rest, nil); err != nil {
		return noDataRound{}, err
	}
	if err := reading.sayPresent(ctx, finished.PresentKeys); err != nil {
		return noDataRound{}, err
	}
	result.absenceStarts, result.said = reading.starts, reading.emitted
	return result, nil
}

// hold reserves, by structure and against the share, what the round's delta
// has grown to and extra more beside it. The delta stays to the Slot's end:
// it is the statement the tail writes.
func (reading *segmentedNoDataRound) hold(ctx context.Context, extra uint64) error {
	reading.extra += extra
	target := reading.round.HeldBytes() + reading.extra
	if target <= reading.charged {
		return nil
	}
	if err := reading.stream.reserveNoDataHeld(ctx, retainPhaseOutput, target-reading.charged); err != nil {
		return err
	}
	reading.charged = target
	return nil
}

// release gives back what the round held beside its delta and holds no
// longer.
func (reading *segmentedNoDataRound) release(bytes uint64) {
	bytes = min(bytes, reading.extra, reading.charged)
	reading.extra -= bytes
	reading.charged -= bytes
	reading.stream.unretain(retainPhaseOutput, bytes)
}

// scan reads the memory's groups a segment at a time, feeding the present
// ones and keeping the other names, then reads the header again.
func (reading *segmentedNoDataRound) scan(ctx context.Context) error {
	if !reading.scanned {
		return nil
	}
	stream, store := reading.stream, reading.stream.coordinator.ports.NoData
	count := stream.coordinator.noDataSegmentGroups()
	reading.absent = map[[sha256.Size]byte]string{}
	// The next page is read while this one is taken (decision-020 0.6.x
	// throughput design 3.1, way 4): its cursor is this page's, so one page
	// ahead, held unreserved until it is taken - a segment's groups at most.
	// What the reads ask by, taken here: the goroutines read no field of
	// the round, which this one goes on changing.
	source, presentAsOf := reading.source, reading.header.PresentAsOf
	scan := func(cursor uint64) <-chan noDataRead[execution.NoDataScanPage] {
		return readAhead(func() (execution.NoDataScanPage, error) {
			return store.ScanNoDataGroups(ctx, execution.NoDataScanRequest{
				Identity: source, Cursor: cursor, Count: count, PresentAsOf: presentAsOf})
		})
	}
	next := scan(0)
	for {
		read := <-next
		page, err := read.value, read.err
		if err != nil {
			return noDataRosterUnread(reading.identity, "", err)
		}
		if err := reading.pageStatus(page.Status, page.ReasonCode); err != nil {
			return err
		}
		if page.Next != 0 {
			next = scan(page.Next)
		}
		held := groupsHeldBytes(page.Groups)
		if err := stream.reserveNoDataHeld(ctx, retainPhaseState, held); err != nil {
			return err
		}
		err = reading.takePage(ctx, page.Groups)
		stream.unretain(retainPhaseState, held)
		if err != nil {
			return err
		}
		if page.Next == 0 {
			break
		}
	}
	// The record read is one record when the header after the scan still
	// names the memory the scan began on: every write moves the revision or,
	// a whole record written over none, starts it again with the digest of
	// what it wrote, and the digest is the fold of the groups themselves. The
	// apply version is left out: equal revision and digest are equal groups,
	// whichever round wrote them.
	again, err := store.RecheckNoDataHeader(ctx, reading.source)
	if err != nil {
		return noDataRosterUnread(reading.identity, "", err)
	}
	if again.Status != execution.NoDataMemoryFound || again.MarkerRevision != reading.header.MarkerRevision ||
		again.PersistedMutationDigest != reading.header.PersistedMutationDigest {
		return &NoDataRosterMovedError{Plan: reading.identity, Reason: fmt.Sprintf(
			"header at revision %d (%s) before the scan, revision %d (%s) after it",
			reading.header.MarkerRevision, reading.header.Status, again.MarkerRevision, again.Status)}
	}
	return nil
}

// pageStatus turns a part the store answered with a failure into the Slot's
// error: one that may be retried fails the Slot, and a record this build
// cannot decode fails only this Plan's round, as a whole load of it does.
func (reading *segmentedNoDataRound) pageStatus(status execution.NoDataLoadStatus, reason execution.ReasonCode) error {
	switch status {
	case execution.NoDataMemoryFound:
		return nil
	case execution.NoDataMemoryUnavailable:
		return noDataRosterUnread(reading.identity, reason, errors.New("the store did not answer"))
	default:
		return derivationFailed(fmt.Errorf("alarmd worker: no-data memory of strategy %s read as %s (%s)",
			reading.due.Identity.StrategyID, status, reason))
	}
}

// takePage feeds a segment's present groups and keeps its other names, each
// once.
func (reading *segmentedNoDataRound) takePage(ctx context.Context, groups []execution.NoDataGroupMemory) error {
	var added uint64
	for _, group := range groups {
		if reading.round.Present(group.GroupKey) {
			reading.resolveHost(group)
			if _, _, err := reading.round.Feed(group); err != nil {
				return derivationFailed(err)
			}
			continue
		}
		digest := sha256.Sum256([]byte(group.GroupKey))
		if _, held := reading.absent[digest]; held {
			continue
		}
		reading.absent[digest] = group.GroupKey
		added += absentNameEntryBytes + uint64(len(group.GroupKey))
	}
	reading.names += added
	return reading.hold(ctx, added)
}

// resolveHost names a remembered history group whose host the index holds
// under another business, before the group is fed: the one fact the whole
// round resolved from the memory before judging it (resolveNoDataHosts), here
// resolved group by group as the memory is read.
func (reading *segmentedNoDataRound) resolveHost(group execution.NoDataGroupMemory) {
	if !reading.history || group.LastSeen == 0 {
		return
	}
	candidate, ok := nodata.HistoryHostCandidate(group.GroupKey)
	if !ok {
		return
	}
	if business, held := reading.hosts.LookupHostBusiness(candidate.Key()); held && business != reading.due.Identity.BusinessID {
		reading.round.MarkOutOfBusiness(group.GroupKey)
	}
}

// readAbsent reads the absent names back a window at a time, in digest
// order, feeds them and says what they say.
func (reading *segmentedNoDataRound) readAbsent(ctx context.Context) error {
	if len(reading.absent) == 0 {
		return nil
	}
	names := make([]absentName, 0, len(reading.absent))
	for digest, name := range reading.absent {
		names = append(names, absentName{digest: digest, name: name})
	}
	reading.absent = nil
	defer func() { reading.release(reading.names); reading.names = 0 }()
	sort.Slice(names, func(left, right int) bool {
		return string(names[left].digest[:]) < string(names[right].digest[:])
	})
	stream, store := reading.stream, reading.stream.coordinator.ports.NoData
	window := stream.coordinator.noDataSegmentGroups()
	keysOf := func(start int) []string {
		part := names[start:min(start+window, len(names))]
		keys := make([]string, len(part))
		for index, name := range part {
			keys[index] = name.name
		}
		return keys
	}
	// The next window is read while this one is fed and said - its series
	// evaluated - one window ahead, as the scan reads its next page.
	source, presentAsOf := reading.source, reading.header.PresentAsOf
	readWindow := func(keys []string) <-chan noDataRead[execution.NoDataGroupsResult] {
		return readAhead(func() (execution.NoDataGroupsResult, error) {
			return store.ReadNoDataGroups(ctx, execution.NoDataGroupsRequest{
				Identity: source, Keys: keys, PresentAsOf: presentAsOf})
		})
	}
	keys := keysOf(0)
	next := readWindow(keys)
	for start := 0; start < len(names); start += window {
		answer := <-next
		read, err := answer.value, answer.err
		current := keys
		if start+window < len(names) {
			keys = keysOf(start + window)
			next = readWindow(keys)
		}
		if err != nil {
			return noDataRosterUnread(reading.identity, "", err)
		}
		if err := reading.pageStatus(read.Status, read.ReasonCode); err != nil {
			return err
		}
		held := groupsHeldBytes(read.Groups)
		if err := stream.reserveNoDataHeld(ctx, retainPhaseState, held); err != nil {
			return err
		}
		err = reading.takeAbsent(ctx, current, read.Groups)
		stream.unretain(retainPhaseState, held)
		if err != nil {
			return err
		}
	}
	return nil
}

// takeAbsent feeds one absence window and says its series.
func (reading *segmentedNoDataRound) takeAbsent(ctx context.Context, keys []string, groups []execution.NoDataGroupMemory) error {
	if len(groups) != len(keys) {
		return &NoDataRosterMovedError{Plan: reading.identity, Reason: "the read by name answered for other groups"}
	}
	var said []nodata.GroupJudgement
	for index, group := range groups {
		if group.GroupKey != keys[index] {
			// Scanned and not there: the record lost a group no write of this
			// Slot removed.
			return &NoDataRosterMovedError{Plan: reading.identity, Reason: fmt.Sprintf("group %q is gone", keys[index])}
		}
		reading.resolveHost(group)
		judgement, judged, err := reading.round.Feed(group)
		if err != nil {
			return derivationFailed(err)
		}
		if judged && judgement.Verdict != "" && judgement.Verdict != nodata.VerdictUnavailable {
			said = append(said, judgement)
		}
	}
	if err := reading.hold(ctx, 0); err != nil {
		return err
	}
	if len(said) == 0 {
		return nil
	}
	if reading.round.Outcome() != nodata.OutcomeEvaluated {
		// A round that may finish unjudged says nothing before it finishes,
		// and only a round judging absence says anything of an absent group:
		// a partial round judges none (A1).
		return derivationFailed(fmt.Errorf("alarmd worker: a %s round judged %d absent groups",
			reading.round.Outcome(), len(said)))
	}
	sort.Slice(said, func(left, right int) bool { return said[left].Key < said[right].Key })
	return reading.say(ctx, said, nil)
}

// say synthesizes the judgements' series and hands them on.
func (reading *segmentedNoDataRound) say(ctx context.Context, judgements []nodata.GroupJudgement, present []string) error {
	var series []nodata.SyntheticSeries
	var err error
	if present != nil {
		series, err = reading.round.SynthesizePresent(present)
	} else {
		series, err = reading.round.Synthesize(judgements)
	}
	if err != nil {
		return outputFailed(err)
	}
	var starts uint64
	for _, synthetic := range series {
		entry, err := reading.stream.noDataCompletedSeries(reading.view, synthetic, reading.version)
		if err != nil {
			return outputFailed(err)
		}
		if synthetic.Value == nodata.AbsentValue && synthetic.FirstAbsent > 0 {
			if reading.starts == nil {
				reading.starts = make(map[string]int64)
				reading.stream.noteAbsenceStarts(reading.due.Identity, reading.starts)
			}
			reading.starts[string(entry.series)] = synthetic.FirstAbsent
			starts += absenceStartEntryBytes
		}
		if err := reading.emit(entry); err != nil {
			return err
		}
		reading.emitted++
	}
	return reading.hold(ctx, starts)
}

// sayPresent says the present groups' series a window at a time.
func (reading *segmentedNoDataRound) sayPresent(ctx context.Context, keys []string) error {
	listed := uint64(len(keys)) * uint64(unsafe.Sizeof(""))
	if err := reading.stream.reserveNoDataHeld(ctx, retainPhaseState, listed); err != nil {
		return err
	}
	defer reading.stream.unretain(retainPhaseState, listed)
	window := reading.stream.coordinator.noDataSegmentGroups()
	for start := 0; start < len(keys); start += window {
		if err := reading.say(ctx, nil, keys[start:min(start+window, len(keys))]); err != nil {
			return err
		}
	}
	return nil
}

// noteAbsenceStarts hands the output a Plan's absence starts as they are
// said, so a window landing before the round finishes checks its absences'
// manual closes as a Slot applied whole does.
func (stream *streamedExecution) noteAbsenceStarts(plan execution.PlanIdentity, starts map[string]int64) {
	if stream.noDataAbsenceStarts == nil {
		stream.noDataAbsenceStarts = make(noDataAbsenceStarts)
	}
	stream.noDataAbsenceStarts[plan] = starts
}

// noDataRead is one read of a no-data memory's parts, answered on its own
// goroutine.
type noDataRead[T any] struct {
	value T
	err   error
}

// readAhead starts one read and answers it on the channel when it is done.
// A read whose answer is never taken - the round ended first - goes with its
// goroutine; the channel holds its one answer.
func readAhead[T any](read func() (T, error)) <-chan noDataRead[T] {
	answered := make(chan noDataRead[T], 1)
	go func() {
		value, err := read()
		answered <- noDataRead[T]{value: value, err: err}
	}()
	return answered
}
