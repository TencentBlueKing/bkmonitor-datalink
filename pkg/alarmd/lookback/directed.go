// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lookback

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Query Group some of whose series were seen late (series_late) is read
// again for every one of its Slots, not only its samples: the directed read.
// Its first read of each Slot is kept as the set of series it had, and at
// the rung its late series were seen at the Slot's frozen physical query is
// read once more - the query itself, not the tail a recheck reads, so the
// read is one the Slot could have made. The series the later read has and
// the first did not are the Slot's late series: kept whole, they are what a
// supplement of the Slot evaluates, handed over as the read they came in.
// One read of the query service serves both the finding and the supplement.
//
// A Slot of more than one physical query is not read: its late series would
// be found in one query and needed from all of them, which one read of each
// in any order cannot keep. It is counted unobserved, multi_query, and a
// supplement of it is not attempted.

// Directed window outcomes, closed: what became of each Slot of a directed
// Query Group. supplemented is a Slot read whose supplement ran, whatever
// each of its series came to; nothing_late one read that had no series its
// first read did not. flight_busy, contract_expired and failed are Slots
// read whose supplement did not run, by why; unobserved one not read, by
// the reason in DirectedUnobservedReasons.
const (
	DirectedSupplemented    = "supplemented"
	DirectedNothingLate     = "nothing_late"
	DirectedFlightBusy      = "flight_busy"
	DirectedContractExpired = "contract_expired"
	DirectedFailed          = "failed"
	DirectedUnobserved      = "unobserved"
)

// DirectedOutcomes is every directed window outcome.
var DirectedOutcomes = []string{DirectedSupplemented, DirectedNothingLate, DirectedFlightBusy, DirectedContractExpired,
	DirectedFailed, DirectedUnobserved}

// Why a directed Slot was not read, closed.
const (
	// UnobservedYielded is a Slot whose read found no permit within its
	// rung's window, or was stopped for a formal query until the window
	// passed.
	UnobservedYielded = "yielded"
	// UnobservedReadFailed is a read that failed or came back incomplete.
	UnobservedReadFailed = "read_failed"
	// UnobservedFirstReadIncomplete is a Slot whose own first read was not
	// complete, or not seen whole: there is nothing to tell late series by.
	UnobservedFirstReadIncomplete = "first_read_incomplete"
	// UnobservedMultiQuery is a Slot of more than one physical query.
	UnobservedMultiQuery = "multi_query"
	// UnobservedMemoryRefused is a Slot whose series set or kept series the
	// process's memory line refused.
	UnobservedMemoryRefused = "memory_refused"
)

// DirectedUnobservedReasons is every reason.
var DirectedUnobservedReasons = []string{UnobservedYielded, UnobservedReadFailed, UnobservedFirstReadIncomplete,
	UnobservedMultiQuery, UnobservedMemoryRefused}

// Series outcomes of the supplements that ran, closed: the names
// execution.SupplementFacts counts, and the points the admitted ones were
// evaluated on.
const (
	SeriesCandidates      = "candidates"
	SeriesAdmitted        = "admitted"
	SeriesCrossedT        = "crossed_t"
	SeriesNoDataFact      = "no_data_fact"
	SeriesConfigDrift     = "config_drift"
	SeriesInputIncomplete = "input_incomplete"
	SeriesWithheld        = "withheld"
)

// SupplementSeriesOutcomes is every series outcome.
var SupplementSeriesOutcomes = []string{SeriesCandidates, SeriesAdmitted, SeriesCrossedT, SeriesNoDataFact,
	SeriesConfigDrift, SeriesInputIncomplete, SeriesWithheld}

// ErrNotKept is a kept read asked to replay a query it did not keep.
var ErrNotKept = errors.New("alarmd lookback: the physical query was not kept")

// SupplementJob is one Slot's late series and the read they came in, for a
// supplement of the Slot. Deadline is when the rung the read was made at
// ends: a supplement not run by then is not tried again.
type SupplementJob struct {
	QueryGroup     execution.QueryGroupIdentity
	EvaluationTime execution.EvaluationTime
	Series         []execution.SeriesIdentityDigest
	Read           *KeptRead
	Deadline       time.Time
	// ReadHoldMillis is the read hold the Slot was frozen with (its first
	// read's contract): the supplement freezes it again with the same one,
	// so it is the contract the Slot ran under, with the same deadlines.
	ReadHoldMillis int64
	// Guard, when set, is asked once the supplement holds its Query Group's
	// flight: false means the group has moved on, and the supplement is
	// refused as SupplementOvertaken having written nothing.
	Guard func() bool
}

// SupplementOvertaken is a supplement refused because its guard said the
// Query Group had moved on by the time it held the flight.
const SupplementOvertaken = "overtaken"

// SupplementOutcome is what became of a SupplementJob: the supplement's own
// facts when it ran, or, when it did not, why - flight_busy,
// contract_expired or failed.
type SupplementOutcome struct {
	Ran     bool
	Facts   execution.SupplementFacts
	Refused string
	// Held is how long the supplement held its Query Group's flight, from
	// taking it to the supplement's return - freezing the contract,
	// evaluating, writing State and any Redis wait inside them: the time its
	// group's own Slot waited behind it. Zero when it never took the flight.
	Held time.Duration
}

// SupplementHoldBuckets are the upper bounds a supplement's hold of its
// Query Group's flight is counted under, the last one unbounded: a Slot
// behind it waits that long, and the bound the design holds it to is
// seconds.
var SupplementHoldBuckets = []string{"le_100ms", "le_500ms", "le_1s", "le_5s", "gt_5s"}

// holdBucket is the bucket a hold falls in.
func holdBucket(held time.Duration) string {
	switch {
	case held <= 100*time.Millisecond:
		return "le_100ms"
	case held <= 500*time.Millisecond:
		return "le_500ms"
	case held <= time.Second:
		return "le_1s"
	case held <= 5*time.Second:
		return "le_5s"
	default:
		return "gt_5s"
	}
}

// KeptRead is the late series of one physical query, as the provider
// delivered them, and the query's completion restated for those series
// alone. It is what access.KeptRead replays.
type KeptRead struct {
	digest     execution.PhysicalQueryDigest
	batches    []execution.ProviderSeriesBatch
	completion execution.ProviderCompletion
}

// Replay delivers the kept series in the order they were read and returns
// the completion restated for them.
func (read *KeptRead) Replay(ctx context.Context, spec execution.PhysicalQuerySpec, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	if read == nil || spec.Digest != read.digest {
		return execution.ProviderCompletion{}, ErrNotKept
	}
	for _, batch := range read.batches {
		if err := sink.ConsumeProviderSeries(ctx, batch); err != nil {
			return execution.ProviderCompletion{}, err
		}
	}
	return read.completion, nil
}

// seriesSetEntryBytes is what one series of a directed first read is
// charged: its key and the map's share of an entry.
const seriesSetEntryBytes = 24

// seriesSet is the series one first read had, grown as the memory line
// admits it the way a series table is.
type seriesSet struct {
	set     map[uint64]struct{}
	admit   func(bytes uint64) bool
	granted int
	refused bool
}

func newSeriesSet(admit func(bytes uint64) bool) *seriesSet {
	return &seriesSet{set: map[uint64]struct{}{}, admit: admit}
}

func (series *seriesSet) add(key uint64) {
	if series.refused {
		return
	}
	if _, known := series.set[key]; known {
		return
	}
	if len(series.set) >= series.granted {
		step := max(series.granted, seriesAdmitFirst)
		if series.admit != nil && !series.admit(uint64(step)*seriesSetEntryBytes) {
			series.set, series.refused = nil, true
			return
		}
		series.granted += step
	}
	series.set[key] = struct{}{}
}

// directedQuery is one physical query of a directed Slot: its frozen spec,
// the series its first read had, and whether that read was whole.
type directedQuery struct {
	spec     execution.PhysicalQuerySpec
	first    *seriesSet
	complete bool
	finished bool
	// slot is the Slot it is a read of, begunAt when it began.
	slot    *directedSlot
	begunAt time.Time
}

// directedSlot is one Slot of a directed Query Group, from its first read
// to its directed read's end.
type directedSlot struct {
	id         uint64
	source     string
	queryGroup execution.QueryGroupIdentity
	evaluation execution.EvaluationTime
	step       time.Duration
	readAt     time.Time
	rung       int
	queries    []*directedQuery
	running    bool
	dropped    bool
	// period is the directed reads the Slot was opened in: a window that
	// ends after they did still supplements its late series, and is not
	// counted into the reports of the reads that came after.
	period *seriesLateState
	// late is how many series its directed reads found late, both of them
	// together, and seenAge how long after its first read the series that
	// had crossed it were seen, at the latest: both set when it is filed.
	late    int
	seenAge time.Duration
	// readyAt is when its first query was ready and following the Slot its
	// schedule has next, which say when the next Slot reads; early is its
	// early read (early.go), nil until its first read ends.
	readyAt   time.Time
	following execution.EvaluationTime
	early     *earlyRead
	// readHold is the read hold its first read's contract was frozen with,
	// which its supplements freeze it with again.
	readHold int64
	// facts is what its supplements came to, both reads' summed, and
	// supplemented whether any ran; filed once it has been counted.
	facts        execution.SupplementFacts
	supplemented bool
	filed        bool
}

// supplementTally is what a directed Query Group's Slots came to since it
// became directed: windows by outcome, the unobserved ones by why, and the
// series outcomes of the supplements that ran.
type supplementTally struct {
	since      time.Time
	windows    map[string]uint64
	unobserved map[string]uint64
	facts      execution.SupplementFacts
	early      map[string]uint64
}

func newSupplementTally(since time.Time) *supplementTally {
	return &supplementTally{since: since, windows: map[string]uint64{}, unobserved: map[string]uint64{}, early: map[string]uint64{}}
}

// captureDirectedLocked keeps a directed Query Group's first read of one of
// its Slots' physical queries.
func (engine *Engine) captureDirectedLocked(state *group, query Query, source string, step time.Duration, now time.Time) *directedQuery {
	if state.directed == nil {
		state.directed = map[execution.EvaluationTime]*directedSlot{}
	}
	if state.supplement == nil {
		state.supplement = newSupplementTally(now)
	}
	slot := state.directed[query.Contract.Slot.EvaluationTime]
	if slot == nil {
		engine.nextID++
		slot = &directedSlot{id: engine.nextID, source: source, queryGroup: query.Contract.Slot.QueryGroup,
			evaluation: query.Contract.Slot.EvaluationTime, step: step, readAt: now, rung: state.seriesLate.rung,
			period: state.seriesLate, readyAt: query.ReadyAt, following: query.FollowingSlot,
			readHold: query.Contract.ReadHoldMillis}
		state.directed[query.Contract.Slot.EvaluationTime] = slot
	}
	captured := &directedQuery{spec: query.Spec, first: newSeriesSet(engine.options.Memory), slot: slot, begunAt: now}
	slot.queries = append(slot.queries, captured)
	if len(slot.queries) > 1 && slot.early != nil && !slot.early.running {
		// A second physical query: its late series could not be read whole.
		engine.fileEarlyLocked(state, slot, EarlyMultiQuery)
	}
	return captured
}

// completeDirectedLocked takes the end of a directed first read: how long
// the read took, among its Query Group's readings when it was whole, and
// its Slot's early read.
func (engine *Engine) completeDirectedLocked(captured *directedQuery, completion execution.ProviderCompletion, err error, now time.Time) {
	captured.finished = true
	captured.complete = err == nil && completion.Completeness == execution.CompletenessFull && !captured.first.refused
	state := engine.groups[captured.slot.queryGroup]
	if state == nil || captured.slot.dropped {
		return
	}
	if captured.complete {
		state.took.add(now.Sub(captured.begunAt))
	}
	engine.planEarlyLocked(state, captured.slot, now)
}

// dueDirectedLocked is the directed Slot of one Query Group to read now,
// if any, after settling the ones that will not be read.
//
// One at a time per Query Group, the oldest first: a supplement of a Slot
// runs after the supplements of the Slots before it, so a series late in
// two Slots in a row is supplemented at the first before the second moves
// its State past it.
func (engine *Engine) dueDirectedLocked(state *group, now time.Time) []*directedSlot {
	var oldest *directedSlot
	running := false
	for evaluation, slot := range state.directed {
		if slot.running {
			running = true
			continue
		}
		if slot.early != nil && !slot.early.done {
			// Read at its rung once its early read has its outcome.
			continue
		}
		at := slot.readAt.Add(rungDelay(slot.rung, slot.step))
		if now.Before(at) {
			continue
		}
		reason := ""
		switch {
		case now.After(at.Add(rungWindow(slot.rung, slot.step))):
			reason = UnobservedYielded
		case len(slot.queries) != 1:
			reason = UnobservedMultiQuery
		case !slot.queries[0].finished || !slot.queries[0].complete:
			reason = UnobservedFirstReadIncomplete
			if slot.queries[0].first.refused {
				reason = UnobservedMemoryRefused
			}
		}
		if reason != "" {
			engine.fileWindowLocked(state, slot, DirectedUnobserved, reason, rungRead{})
			delete(state.directed, evaluation)
			continue
		}
		if oldest == nil || slot.evaluation < oldest.evaluation {
			oldest = slot
		}
	}
	if running || oldest == nil {
		return nil
	}
	return []*directedSlot{oldest}
}

// noteDirectedLocked counts a directed Slot's end on its Query Group and
// its source.
func (engine *Engine) noteDirectedLocked(state *group, slot *directedSlot, outcome, reason string, facts *execution.SupplementFacts) {
	engine.counts.directedWindows[key2(slot.source, outcome)]++
	engine.noteLateSeriesLocked(state, slot, outcome, facts)
	if state.supplement == nil {
		state.supplement = newSupplementTally(engine.options.Now())
	}
	state.supplement.windows[outcome]++
	if outcome == DirectedUnobserved {
		engine.counts.directedUnobserved[key2(slot.source, reason)]++
		state.supplement.unobserved[reason]++
	}
	if facts == nil {
		return
	}
	engine.counts.supplementPoints[slot.source] += facts.Points
	for outcome, n := range seriesOutcomes(*facts) {
		engine.counts.supplementSeries[key2(slot.source, outcome)] += uint64(n)
	}
	sum := &state.supplement.facts
	sum.Candidates += facts.Candidates
	sum.Admitted += facts.Admitted
	sum.Points += facts.Points
	sum.CrossedT += facts.CrossedT
	sum.NoDataFact += facts.NoDataFact
	sum.ConfigDrift += facts.ConfigDrift
	sum.InputIncomplete += facts.InputIncomplete
	sum.Withheld += facts.Withheld
}

func seriesOutcomes(facts execution.SupplementFacts) map[string]int {
	return map[string]int{SeriesCandidates: facts.Candidates, SeriesAdmitted: facts.Admitted, SeriesCrossedT: facts.CrossedT,
		SeriesNoDataFact: facts.NoDataFact, SeriesConfigDrift: facts.ConfigDrift, SeriesInputIncomplete: facts.InputIncomplete,
		SeriesWithheld: facts.Withheld}
}

// keptSink keeps, of a directed read, the series its first read did not
// have, and counts the bytes the read delivered.
type keptSink struct {
	first      map[uint64]struct{}
	admit      func(bytes uint64) bool
	bytes      uint64
	batches    []execution.ProviderSeriesBatch
	delivery   execution.SeriesDelivery
	series     map[execution.SeriesIdentityDigest]struct{}
	stats      execution.ProviderStats
	refused    bool
	invalid    error
	keptBytes  uint64
	grantBytes uint64
}

func (sink *keptSink) ConsumeProviderSeries(_ context.Context, batch execution.ProviderSeriesBatch) error {
	sink.bytes += batch.Delivery.Bytes
	if sink.refused || sink.invalid != nil || batch.Dataset == nil || batch.Dataset.Len() == 0 {
		return nil
	}
	record, _ := batch.Dataset.Record(0)
	identity := record.DimensionIdentityDigest()
	if _, had := sink.first[hashString(identity)]; had {
		return nil
	}
	// What a kept series holds is the delivery's own byte count; the line is
	// asked for room as it doubles, as a series table's is.
	if sink.keptBytes+batch.Delivery.Bytes > sink.grantBytes {
		step := max(sink.grantBytes, sink.keptBytes+batch.Delivery.Bytes)
		if sink.admit != nil && !sink.admit(step) {
			sink.refused, sink.batches, sink.series = true, nil, nil
			return nil
		}
		sink.grantBytes += step
	}
	delivery, err := execution.AccumulateSeriesDelivery(sink.delivery, batch.Delivery)
	if err != nil {
		sink.invalid = err
		return nil
	}
	sink.delivery, sink.keptBytes = delivery, sink.keptBytes+batch.Delivery.Bytes
	sink.batches = append(sink.batches, batch)
	sink.stats.Series++
	sink.stats.Records += batch.Delivery.Records
	if sink.series == nil {
		sink.series = map[execution.SeriesIdentityDigest]struct{}{}
	}
	sink.series[execution.SeriesIdentityDigest(identity)] = struct{}{}
	return nil
}

// kept is the read the sink kept for the query, and its late series sorted.
func (sink *keptSink) kept(spec execution.PhysicalQuerySpec, completion execution.ProviderCompletion) (*KeptRead, []execution.SeriesIdentityDigest) {
	restated := completion
	restated.Delivery, restated.Stats = sink.delivery, sink.stats
	restated.DataState = execution.DataStateData
	if len(sink.batches) == 0 {
		restated.DataState, restated.Delivery = execution.DataStateEmpty, execution.SeriesDelivery{}
	}
	series := make([]execution.SeriesIdentityDigest, 0, len(sink.series))
	for identity := range sink.series {
		series = append(series, identity)
	}
	sort.Slice(series, func(i, j int) bool { return series[i] < series[j] })
	if len(series) > execution.MaxSupplementSeries {
		series = series[:execution.MaxSupplementSeries]
	}
	return &KeptRead{digest: spec.Digest, batches: sink.batches, completion: restated}, series
}

// directedRead reads a directed Slot's query again and hands its late
// series to the supplement. The permit is given back before the supplement
// runs: a supplement reads nothing, and it waits for nothing but its own
// Query Group's flight.
func (engine *Engine) directedRead(ctx context.Context, slot *directedSlot, release func(), yield <-chan struct{}) {
	readCtx, cancel := context.WithTimeout(ctx, RecheckTimeout)
	back := make(chan struct{})
	if yield != nil {
		go func() {
			select {
			case <-yield:
				cancel()
			case <-back:
			}
		}()
	}
	captured := slot.queries[0]
	engine.mu.Lock()
	first := captured.first.set
	engine.mu.Unlock()
	sink := &keptSink{first: first, admit: engine.options.Memory}
	completion, err := engine.options.Recheck(readCtx, captured.spec, sink)
	close(back)
	release()
	cancel()
	engine.mu.Lock()
	engine.counts.directedBytes[slot.source] += sink.bytes
	state := engine.groups[slot.queryGroup]
	if slot.dropped || state == nil {
		engine.mu.Unlock()
		return
	}
	if (err != nil || completion.Completeness != execution.CompletenessFull) && closed(yield) {
		// Stopped for a formal query: tried again within its window.
		slot.running = false
		engine.mu.Unlock()
		return
	}
	reason := ""
	switch {
	case err != nil || completion.Completeness != execution.CompletenessFull || sink.invalid != nil:
		reason = UnobservedReadFailed
	case sink.refused:
		reason = UnobservedMemoryRefused
	}
	if reason != "" {
		engine.fileWindowLocked(state, slot, DirectedUnobserved, reason, rungRead{})
		delete(state.directed, slot.evaluation)
		engine.mu.Unlock()
		return
	}
	read, series := sink.kept(captured.spec, completion)
	if len(series) == 0 {
		engine.fileWindowLocked(state, slot, DirectedNothingLate, "", rungRead{})
		delete(state.directed, slot.evaluation)
		engine.mu.Unlock()
		return
	}
	deadline := slot.readAt.Add(rungDelay(slot.rung, slot.step) + rungWindow(slot.rung, slot.step))
	engine.mu.Unlock()

	outcome := engine.options.Supplement(ctx, SupplementJob{QueryGroup: slot.queryGroup, EvaluationTime: slot.evaluation,
		ReadHoldMillis: slot.readHold,
		Series:         series, Read: read, Deadline: deadline})

	engine.mu.Lock()
	defer engine.mu.Unlock()
	state = engine.groups[slot.queryGroup]
	if state == nil || slot.dropped {
		return
	}
	delete(state.directed, slot.evaluation)
	engine.noteHoldLocked(state, slot.source, outcome.Held)
	rung := rungRead{late: sink.series}
	switch {
	case outcome.Ran:
		rung.facts = &outcome.Facts
		engine.fileWindowLocked(state, slot, DirectedSupplemented, "", rung)
	case outcome.Refused == DirectedFlightBusy || outcome.Refused == DirectedContractExpired:
		engine.fileWindowLocked(state, slot, outcome.Refused, "", rung)
	default:
		engine.fileWindowLocked(state, slot, DirectedFailed, "", rung)
	}
}

// earlyRefusalWindow is the window outcome of an early read whose supplement
// did not run: one the next Slot's read beat ran out of its time as one past
// its deadline did. ok is false for an early read that came to anything else.
func earlyRefusalWindow(early string) (outcome string, ok bool) {
	switch early {
	case EarlyFlightBusy:
		return DirectedFlightBusy, true
	case EarlyContractExpired, EarlyOvertaken:
		return DirectedContractExpired, true
	case EarlyFailed:
		return DirectedFailed, true
	}
	return "", false
}

// rungRead is what a Slot's read at its rung came to, for filing it: the
// late series it read, and its supplement's facts when one ran.
type rungRead struct {
	late  map[execution.SeriesIdentityDigest]struct{}
	facts *execution.SupplementFacts
}

// fileWindowLocked counts a directed Slot once, when both its reads have
// their outcomes: outcome and reason are its read at the rung's. A Slot
// either of whose supplements ran is supplemented, on both supplements'
// facts; one read with nothing late by both reads is nothing_late; any
// other is what its read at the rung came to. Its late count is both
// reads' series together, and how long after its first read the series
// that had crossed it were seen is the latest read that saw one, no later
// than the next Slot's read: a series that crossed the Slot was in it.
func (engine *Engine) fileWindowLocked(state *group, slot *directedSlot, outcome, reason string, rung rungRead) {
	if slot.filed {
		return
	}
	slot.filed = true
	if slot.early == nil {
		// Its first read never ended: there was nothing to read it early by.
		engine.fileEarlyLocked(state, slot, EarlyFirstReadIncomplete)
	}
	if rung.facts != nil {
		slot.facts = addFacts(slot.facts, *rung.facts)
		slot.supplemented = true
	}
	var earlyLate map[uint64]struct{}
	if slot.early.late != nil {
		earlyLate = slot.early.late.set
	}
	late, unseen := len(earlyLate), false
	for identity := range rung.late {
		if _, seen := earlyLate[hashString(string(identity))]; !seen {
			late++
			unseen = true
		}
	}
	slot.late = late
	age, crossed := time.Duration(0), false
	if slot.early.crossed {
		age, crossed = slot.early.readAt.Sub(slot.readAt), true
	}
	if rung.facts != nil && rung.facts.CrossedT > 0 {
		seen := rungDelay(slot.rung, slot.step)
		if !unseen && !slot.early.readAt.IsZero() {
			// Every series it supplemented the early read had seen already.
			seen = slot.early.readAt.Sub(slot.readAt)
		}
		age, crossed = max(age, seen), true
	}
	if !crossed {
		age = rungDelay(slot.rung, slot.step)
	}
	if !slot.early.next.IsZero() {
		age = min(age, slot.early.next.Sub(slot.readAt))
	}
	slot.seenAge = age
	var facts *execution.SupplementFacts
	switch {
	case slot.supplemented:
		outcome, reason = DirectedSupplemented, ""
		summed := slot.facts
		facts = &summed
	case outcome == DirectedNothingLate && len(earlyLate) > 0:
		// The early read found late series and its supplement did not run,
		// and the read at the rung found none: not a Slot with nothing late,
		// but one whose supplement did not run, by the early read's why.
		if refused, ok := earlyRefusalWindow(slot.early.outcome); ok {
			outcome = refused
		}
	}
	engine.noteDirectedLocked(state, slot, outcome, reason, facts)
}

// SupplementReading is one directed Query Group's standing: the rung it is
// read at, since when, its Slots by outcome and the unobserved ones by why,
// the series outcomes of its supplements, and its coverage - Slots
// supplemented over those supplemented and those not read. A Slot read with
// nothing late is not a Slot to supplement and is in neither; the Slots
// whose supplement did not run are counted beside it, by why.
type SupplementReading struct {
	QueryGroup execution.QueryGroupIdentity `json:"query_group"`
	Source     string                       `json:"source"`
	Rung       string                       `json:"rung"`
	Since      time.Time                    `json:"since"`
	Windows    map[string]uint64            `json:"windows"`
	Unobserved map[string]uint64            `json:"unobserved"`
	Series     execution.SupplementFacts    `json:"series"`
	Coverage   float64                      `json:"coverage"`
	Pending    int                          `json:"pending"`
	// Early is its Slots by what their early read came to (EarlyOutcomes).
	// LeadSeconds is how long before its next Slot reads an early read of it
	// starts at the latest now: TookMaxSeconds, the longest of its last
	// TookReadings first reads, and HoldMaxSeconds, the longest of its last
	// HoldReadings supplement holds, or of the process's while it has none.
	Early          map[string]uint64 `json:"early"`
	LeadSeconds    float64           `json:"lead_seconds"`
	TookMaxSeconds float64           `json:"took_max_seconds"`
	HoldMaxSeconds float64           `json:"hold_max_seconds"`
	TookReadings   int               `json:"took_readings"`
	HoldReadings   int               `json:"hold_readings"`
}

// SupplementReading returns this owned group's cumulative directed evidence.
// The HTTP pager selects groups; this read never issues a provider query.
func (engine *Engine) SupplementReading(queryGroup execution.QueryGroupIdentity) (SupplementReading, bool) {
	if !engine.options.Owns(queryGroup) {
		return SupplementReading{}, false
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	state := engine.groups[queryGroup]
	if state == nil {
		return SupplementReading{}, false
	}
	return engine.supplementReadingLocked(queryGroup, state)
}

// supplementReadingLocked is a group's standing, or false for a group that
// is not directed and never was.
func (engine *Engine) supplementReadingLocked(queryGroup execution.QueryGroupIdentity, state *group) (SupplementReading, bool) {
	tally := state.supplement
	if tally == nil {
		return SupplementReading{}, false
	}
	lead := engine.leadLocked(state)
	hold := lead - state.took.longest()
	reading := SupplementReading{QueryGroup: queryGroup, Source: state.source, Since: tally.since,
		Windows: map[string]uint64{}, Unobserved: map[string]uint64{}, Series: tally.facts, Pending: len(state.directed),
		Early: map[string]uint64{}, LeadSeconds: lead.Seconds(), TookMaxSeconds: state.took.longest().Seconds(),
		HoldMaxSeconds: hold.Seconds(), TookReadings: state.took.count, HoldReadings: state.holds.count}
	for _, outcome := range EarlyOutcomes {
		reading.Early[outcome] = tally.early[outcome]
	}
	if state.seriesLate != nil {
		reading.Rung = RungNames[state.seriesLate.rung]
	}
	for _, outcome := range DirectedOutcomes {
		reading.Windows[outcome] = tally.windows[outcome]
	}
	for _, reason := range DirectedUnobservedReasons {
		reading.Unobserved[reason] = tally.unobserved[reason]
	}
	if supplemented, unobserved := tally.windows[DirectedSupplemented], tally.windows[DirectedUnobserved]; supplemented+unobserved > 0 {
		reading.Coverage = float64(supplemented) / float64(supplemented+unobserved)
	}
	return reading, true
}
