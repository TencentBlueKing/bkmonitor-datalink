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
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"time"

	accessuq "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// controlPlaneExpectation reads the authoritative object set from the control
// plane. Every replica can read it, which is the point: only the control leader
// observes the whole active set in memory, so a locally derived denominator
// would have most replicas reporting full coverage of nothing.
type controlPlaneExpectation struct {
	repository *controlplane.RedisCatalogRepository
}

func (source controlPlaneExpectation) Expectation(ctx context.Context) (fleet.Expectation, error) {
	if source.repository == nil {
		return fleet.Expectation{}, errors.New("alarmd fleet: control plane repository is required")
	}
	activation, err := source.repository.LoadActivationHead(ctx)
	if err != nil {
		return fleet.Expectation{}, err
	}
	groups, err := source.repository.LoadActiveQueryGroupSet(ctx, activation.ActiveQGSetRef)
	if err != nil {
		return fleet.Expectation{}, err
	}
	// The set is already in hand; returning only its size is what left the
	// coverage gap unable to say which kind of disagreement it had.
	ids := make([]string, 0, len(groups))
	for _, group := range groups {
		ids = append(ids, string(group))
	}
	sort.Strings(ids)
	return fleet.Expectation{QueryGroups: len(groups), Known: true, IDs: ids,
		ActivationRecordRevision: activation.RecordRevision}, nil
}

// registryReplicas lists the workers that should have published a snapshot.
// Using the ownership registry rather than a second index keeps one answer to
// "who is in this deployment" instead of two that can disagree.
type registryReplicas struct {
	store *ownership.RedisStore
}

func (source registryReplicas) ReadyReplicas(ctx context.Context, at time.Time) ([]string, error) {
	if source.store == nil {
		return nil, errors.New("alarmd fleet: ownership store is required")
	}
	workers, _, err := source.store.ListReadyWorkers(ctx, at)
	if err != nil {
		return nil, err
	}
	replicas := make([]string, 0, len(workers))
	for _, worker := range workers {
		replicas = append(replicas, worker.WorkerID)
	}
	return replicas, nil
}

// observationWindowApplier turns the windows other people opened into what this
// replica actually records.
//
// The cap is applied again here even though opening a window already checks it,
// because two windows opened at the same moment each see the count before the
// other's write. Selecting past the budget would be rejected outright and leave
// the replica observing nothing, so the excess is trimmed and reported instead:
// someone opened a window and is waiting for output, and no output plus no
// explanation is the worst answer this can give.
type observationWindowApplier struct {
	store   *fleet.WindowStore
	flow    *observability.TargetFlow
	samples *observability.SeriesSampler
	now     func() time.Time
	observe func(applied, requested, dropped int, err error)
}

func (applier observationWindowApplier) applyOnce(ctx context.Context) {
	windows, err := applier.store.Load(ctx, applier.now())
	if err != nil {
		if applier.observe != nil {
			applier.observe(0, 0, 0, err)
		}
		return
	}
	if applier.samples != nil {
		_ = applier.samples.Select(fleet.SampleSelections(windows))
	}
	requested := fleet.QueryGroups(windows)
	selection := make([]string, 0, observability.TargetFlowMaxGroups)
	seen := make(map[string]struct{}, observability.TargetFlowMaxGroups)
	for _, queryGroup := range requested {
		if _, duplicate := seen[queryGroup]; duplicate {
			continue
		}
		if len(selection) >= observability.TargetFlowMaxGroups {
			break
		}
		seen[queryGroup] = struct{}{}
		selection = append(selection, queryGroup)
	}
	dropped := 0
	for _, queryGroup := range requested {
		if _, kept := seen[queryGroup]; !kept {
			dropped++
		}
	}
	if err := applier.flow.Select(selection); err != nil {
		if applier.observe != nil {
			applier.observe(0, len(requested), dropped, err)
		}
		return
	}
	if applier.observe != nil {
		applier.observe(len(selection), len(requested), dropped, nil)
	}
}

// fleetPublisher writes this replica's snapshot when the bundle's maintenance
// loop asks it to. The cadence lives with that loop rather than here, so there
// is one timer for the job instead of two that can drift apart.
type fleetPublisher struct {
	tracker *fleet.Tracker
	store   *fleet.RedisStore
	replica string
	// startedAt is this process's start, captured once. It bounds every
	// duration this replica reports: a run it watched begin cannot predate it.
	startedAt time.Time
	// build is what this process was built from, the same facts as its
	// build_info series. Published on every snapshot so the page can say which
	// build each replica's numbers came from.
	build fleet.BuildFacts
	owned func() []execution.QueryGroupIdentity
	now   func() time.Time
	// observe reports each publish outcome. A failure is retried on the next
	// tick rather than propagated: the snapshot is diagnostics, and diagnostics
	// must not be able to stop the pipeline whose facts they describe.
	observe func(error)
	// restore reads an owned object's persisted Progress so a replica that has
	// just started can speak for it without waiting to watch a fresh round.
	// Nil disables it, and the replica then reports every object as unknown
	// until each completes one -- the behaviour a restart used to force.
	// It reads a batch at a time, a state or an error per object, in order.
	restore func(context.Context, []execution.QueryGroupIdentity) ([]fleet.RestoredState, []error)
	// contentScope is the content the owned object's Runner runs now, from
	// its lease: what a record's word about an empty round is restored
	// against (fleet.RestoredState.ContentScopeNow). Nil, or empty for an
	// object, restores none of it.
	contentScope func(execution.QueryGroupIdentity) string
	// staleAfter is how far behind an object's Progress cursor may be before
	// its persisted completion stops being evidence about now.
	staleAfter time.Duration
	// restoreBudget bounds how many objects one publish may restore
	// (fleetRestoreBudgetPerPublish). Reading every owned object at once would
	// turn every restart into a burst against the control plane at exactly the
	// moment the process is least settled; spreading it over the publish
	// ticks costs a few more seconds of unknown and no burst at all.
	restoreBudget int
	// capacity reports this replica's own limits and how much of them is in
	// use, so the page can answer "how close are we" from the same read that
	// produced the verdict instead of waiting on collection.
	capacity func() *fleet.Capacity
	// readEarly is the late-data lookback's report of the owned objects read
	// before their data was complete, by object. Nil on a process that runs
	// no lookback, and the snapshot then carries no such line.
	readEarly func() map[string]fleet.ReadEarlyFacts
	readHolds func() map[string]fleet.ReadHoldFacts
	// holdOf is a Query Group's read hold as the lookback's group page knows
	// it, for the overdue episodes; nil without read holds, and an episode's
	// hold is then unknown. onOverdue hears an episode begin (began) and end.
	holdOf    func(string) (int64, bool)
	onOverdue func(episode fleet.OverdueEpisode, began bool)
	// overdueOpen is the episodes begun and not ended, by object;
	// overdueEpisodes the latest ended ones, at most fleet.MaxOverdueEpisodes,
	// which the next snapshot carries. Only publishOnce touches them.
	overdueOpen     map[string]fleet.OverdueEpisode
	overdueEpisodes []fleet.OverdueEpisode
	// periodUnknownSince is when each object the overdue facts name without
	// a period was first seen so, kept while it is (notePeriodUnknown).
	periodUnknownSince map[string]time.Time
	// lateSeries is what the lookback's supplements could not recover, by
	// kind; nil without a lookback.
	lateSeries func() (map[string]fleet.LatePastRoundFacts, map[string]fleet.LateSeriesMissedFacts)
	// applied is the Activation record revision this replica executes by,
	// published on every snapshot so the page can compare it with what the
	// control plane published.
	applied func() uint64
	// overdue is the scheduler's due index, asked which owned objects are past
	// a wake time nothing corrected. Nil on a deployment with no index, and the
	// snapshot then carries no overdue facts at all -- which is a different
	// answer from "none are overdue" and has to stay one.
	overdue fleet.OverdueWakeSource
	// schedule is the same due index, asked where every owned object is in its
	// cycle and how the rounds have been finishing. Nil with overdue, and the
	// snapshot then carries no census and no wake facts on its rows.
	schedule fleet.ScheduleSource
	// strategies names the strategies behind a Query Group, so an overdue
	// object arrives in the list identified the way every other anomaly is. A
	// row nobody can trace back to a strategy is a row nobody can act on.
	strategies func(string) []fleet.StrategyRef
	// dispatch reports what the due index held back. It is read from the
	// replica rather than from a time series because the page's only path to a
	// series can answer "absent" for a series that exists, and the conclusion
	// that failure would produce here is "suppression is not running".
	//
	// Nil on a build that suppresses nothing, which is what makes the overdue
	// count above readable: without it, zero overdue on a shadow build and zero
	// overdue on a healthy one are the same reading.
	dispatch func() *fleet.DispatchSuppression
	// restoreAttempts is scoped to current ownership. The maximum also marks
	// successful reads (including missing/stale history) as finished.
	restoreAttempts map[execution.QueryGroupIdentity]int
	// openAlerts reports the state of this replica's copy of the consumer's
	// open alert set. Nil on a build without the gate, and the snapshot then
	// carries no facts, which the aggregate keeps apart from a copy that is
	// fine.
	openAlerts func() *fleet.OpenAlertSetFacts
	// controlSource reports the state of this replica's control source
	// refresh. Nil on a bundle that has none, and the snapshot then carries
	// no facts.
	controlSource func() *fleet.ControlSourceFacts
	// activation reports the control leader's standing on bringing the
	// fleet's activation to the current publication. Nil on a replica that
	// has not attempted it, which is every follower; the aggregate then
	// takes the one replica that has.
	activation func() *fleet.ActivationFacts
	// activationHeader is a missing activation header the control leader has
	// not written back; nil on every follower and while the header is there.
	activationHeader func() *fleet.ActivationHeaderFacts
	// source and endpoints are the leader's last source round and every
	// replica's resolved external systems; both nil-safe, both optional so
	// the tests that build a publisher by hand keep working.
	source    func() *fleet.SourceFacts
	endpoints func() []fleet.Endpoint
	// readiness reports this replica's own readiness, bit by bit, from the
	// same source its readiness endpoint answers from. Nil-safe and optional
	// like the two above.
	readiness func() *fleet.ReadinessFacts
	// declines reports the Query Groups this replica declines while an
	// execution of each that hung here has not returned. Optional.
	declines func() *fleet.DeclineFacts
	// rebalance reports the control leader's latest rebalance planning
	// round: how the ready replicas hold the objects and what the round
	// would move. Nil on a follower; the aggregate takes the newest round
	// any replica published.
	rebalance func() *fleet.RebalanceFacts
	// assignmentScope reports the control leader's latest reconcile round's
	// census of the content scope on the Assignment records. Nil on a
	// follower, like rebalance.
	assignmentScope func() *fleet.AssignmentScopeFacts
	// assignmentSweep reports the control leader's last sweep of retired
	// Assignment records. Nil on a follower.
	assignmentSweep func() *fleet.AssignmentSweepFacts
	// leaderRound reports the control leader's last reconcile round, stage
	// by stage; nil on a follower.
	leaderRound func() *fleet.LeaderRoundFacts
	// viewStream reports this replica's account of the view stream: the
	// Leader's ledger, or Leading false. Nil on a runtime without the stream.
	viewStream func() *fleet.ViewStreamFacts
	// platformSettings reports the state of this replica's copy of the
	// platform's settings. Nil on a bundle that has none.
	platformSettings func() *fleet.PlatformSettingsFacts
	// publicSurface is how the public surface came out at assembly; it does
	// not change under a running process.
	publicSurface publicSurfaceStanding
	// outputProtocol is the wire format choice this process runs with, read
	// once from its configuration at assembly: the configuration does not
	// change under a running process, and what it decides is frozen into
	// each Plan by the control leader anyway. Nil on a publisher built by
	// hand, and the snapshot then carries no choice.
	outputProtocol *fleet.OutputProtocolFacts
	// retention is the retention lengths this process runs with and their
	// inputs, read once at assembly for the same reason. Nil on a publisher
	// built by hand, and the snapshot then carries none.
	retention *observability.RuntimeRetentionFacts
}

// fleetOverdueWakeCeiling bounds how many parked objects one publish carries.
//
// A snapshot is diagnostics and has to stay a bounded write: after a fail-open
// tick every owned object is briefly past its wake time, and publishing all of
// them would turn the worst moment for the deployment into the largest write
// this replica makes. The true count travels alongside, so the page reports how
// many there are even when it can only name some of them.
const fleetOverdueWakeCeiling = 50

// publisherOverdue asks the due index which owned objects are past a wake time
// nothing corrected, and turns them into list entries.
//
// It is a function rather than a few lines inside publishOnce because that
// method writes to Redis, and the one decision worth pinning here -- that a
// deployment with no index reports absence rather than zero -- would then only
// be reachable through a store. Absence and zero are the two answers this whole
// signal exists to keep apart, so the distinction has to be testable without
// standing up anything.
func publisherOverdue(
	source fleet.OverdueWakeSource,
	at time.Time,
	replica string,
	strategies func(string) []fleet.StrategyRef,
) ([]fleet.Anomaly, *fleet.OverdueFacts) {
	if source == nil {
		return nil, nil
	}
	wakes, total := source.OverdueWakes(at, fleetOverdueWakeCeiling)
	anomalies, facts := fleet.OverdueAnomalies(wakes, total, at, replica, strategies)
	return anomalies, &facts
}

// restoreOwned seeds owned objects without conclusive evidence, and reads
// once the record of each one determined here without records, whose run
// of empty rounds the record may date from before this process started.
//
// It runs before the snapshot is built, so the first publish after a restart
// already carries what the control plane knew, instead of reporting the whole
// deployment as unknown until the slowest object comes round again.
func (publisher *fleetPublisher) restoreOwned(ctx context.Context, owned []execution.QueryGroupIdentity, at time.Time) {
	if publisher.restore == nil || publisher.restoreBudget <= 0 {
		return
	}
	if publisher.restoreAttempts == nil {
		publisher.restoreAttempts = make(map[execution.QueryGroupIdentity]int, len(owned))
	}
	wanted := make([]execution.QueryGroupIdentity, 0, min(len(owned), publisher.restoreBudget))
	for _, queryGroup := range owned {
		if len(wanted) >= publisher.restoreBudget {
			break
		}
		if publisher.restoreAttempts[queryGroup] >= fleetRestoreMaxAttempts {
			continue
		}
		// An object this process has already determined is still read once
		// while it has not seen records: its record may date its run of empty
		// rounds from before this process started.
		if !publisher.tracker.WantsRestore(string(queryGroup)) {
			publisher.restoreAttempts[queryGroup] = fleetRestoreMaxAttempts
			continue
		}
		wanted = append(wanted, queryGroup)
	}
	if len(wanted) == 0 {
		return
	}
	// One read for all of them: the publish waits on one batch, not on a
	// round trip per object. The read answers for a prefix of them -- as far
	// as the memory line had room -- and an object it did not get to is no
	// attempt spent: it is the next publish's. Nor is one whose record the
	// read did not get -- Redis answering its key with an error, or the read
	// not reaching Redis (errRestoreUnread): a Redis loading its dataset for
	// three publishes would otherwise use up every object's attempts, and
	// none would be restored in this term of ownership.
	states, errs := publisher.restore(ctx, wanted)
	for index, queryGroup := range wanted {
		if index >= len(errs) || index >= len(states) {
			break
		}
		var unread *errRestoreUnread
		switch {
		case errs[index] == nil:
			publisher.restoreAttempts[queryGroup] = fleetRestoreMaxAttempts
			state := states[index]
			if publisher.contentScope != nil {
				state.ContentScopeNow = publisher.contentScope(queryGroup)
			}
			publisher.tracker.Restore(string(queryGroup), state, at, publisher.staleAfter)
		case errors.As(errs[index], &unread):
		default:
			publisher.restoreAttempts[queryGroup]++
		}
	}
}

func (publisher *fleetPublisher) publishOnce(ctx context.Context) {
	snapshot := publisher.snapshot(ctx)
	// The outcome is reported either way, including success. Reporting only
	// failures would leave the observer unable to tell recovery from silence,
	// and silence is exactly what a broken publisher produces.
	// With its summary and owned list, which the health route and the
	// verdict read instead of every snapshot; the rows are decided by the
	// same bound the readers decide by.
	summary, err := publisher.store.PublishSummarized(ctx, snapshot, publisher.staleAfter)
	if err == nil {
		publisher.noteOverdue(summary.Part.Overdue, publisher.overdueHidden(snapshot, summary.Part), snapshot.TakenAt)
	}
	if publisher.observe != nil {
		publisher.observe(err)
	}
}

// noteOverdue takes the objects this publish found overdue: one not overdue
// at the last begins an episode, with its read hold now; one overdue at the
// last and not now ends its episode, kept for the next snapshots. An object
// handed to another replica is no longer on this one's rows and ends here.
// One the rows may have left out while it is still overdue (hidden) is not
// overdue no more, and its episode stays open: ended, it would begin and be
// counted again when it is back on them.
func (publisher *fleetPublisher) noteOverdue(objects []fleet.OverdueObject, hidden func(string) bool, at time.Time) {
	current := make(map[string]bool, len(objects))
	for _, object := range objects {
		current[object.QueryGroup] = true
		if _, open := publisher.overdueOpen[object.QueryGroup]; open {
			continue
		}
		episode := fleet.OverdueEpisode{OverdueObject: object, Replica: publisher.replica, Onset: at}
		if publisher.holdOf != nil {
			episode.ReadHoldMillis, episode.ReadHoldKnown = publisher.holdOf(object.QueryGroup)
		}
		if publisher.overdueOpen == nil {
			publisher.overdueOpen = map[string]fleet.OverdueEpisode{}
		}
		publisher.overdueOpen[object.QueryGroup] = episode
		if publisher.onOverdue != nil {
			publisher.onOverdue(episode, true)
		}
	}
	for queryGroup, episode := range publisher.overdueOpen {
		if current[queryGroup] || (hidden != nil && hidden(queryGroup)) {
			continue
		}
		delete(publisher.overdueOpen, queryGroup)
		episode.Clear = at
		publisher.overdueEpisodes = append(publisher.overdueEpisodes, episode)
		if extra := len(publisher.overdueEpisodes) - fleet.MaxOverdueEpisodes; extra > 0 {
			publisher.overdueEpisodes = append([]fleet.OverdueEpisode(nil), publisher.overdueEpisodes[extra:]...)
		}
		if publisher.onOverdue != nil {
			publisher.onOverdue(episode, false)
		}
	}
}

// overdueHidden says which objects a publish's rows may have left out while
// they are still overdue; nil when the rows are whole. A row column cut to
// its budget may have left out any of them. The due index lists only its
// oldest wakes, and counts every wake passed, the rounds out and not yet
// back with them; past that list, an object is still overdue when the index
// holds its wake a whole period late. Asked only of the objects with an
// episode open and not on the rows, one index read each.
func (publisher *fleetPublisher) overdueHidden(snapshot fleet.Snapshot, part fleet.ReplicaPart) func(string) bool {
	switch {
	case part.Truncated != 0:
		return func(string) bool { return true }
	case snapshot.Overdue == nil || !snapshot.Overdue.Truncated:
		return nil
	case publisher.schedule == nil:
		return func(string) bool { return true }
	}
	at := snapshot.TakenAt
	return func(queryGroup string) bool {
		wake := publisher.schedule.WakeOf(queryGroup)
		return wake.Known && fleet.OverdueWake{QueryGroup: queryGroup, WakeAt: wake.DueAt, IntervalSeconds: wake.IntervalSeconds}.LateAt(at)
	}
}

// notePeriodUnknown dates the objects the overdue facts name without a
// period: each one first seen now is since now, and one named before keeps
// when it was first seen. Every one the index returned is dated, then the
// facts keep the longest without a period (fleet.LongestWithoutPeriod), so
// one past that cut is still dated when it is first counted. One no longer
// named is forgotten -- its period arrived, or the index no longer holds it
// -- unless the index's list was cut and the index still holds it without a
// period: past the list, it is not named and still has none.
func (publisher *fleetPublisher) notePeriodUnknown(facts *fleet.OverdueFacts, at time.Time) {
	if facts == nil {
		publisher.periodUnknownSince = nil
		return
	}
	named := make(map[string]bool, len(facts.PeriodUnknownObjects))
	for index := range facts.PeriodUnknownObjects {
		object := &facts.PeriodUnknownObjects[index]
		since, known := publisher.periodUnknownSince[object.QueryGroup]
		if !known {
			if publisher.periodUnknownSince == nil {
				publisher.periodUnknownSince = map[string]time.Time{}
			}
			since = at
			publisher.periodUnknownSince[object.QueryGroup] = since
		}
		object.Since, named[object.QueryGroup] = since, true
	}
	facts.PeriodUnknownObjects = fleet.LongestWithoutPeriod(facts.PeriodUnknownObjects)
	for queryGroup := range publisher.periodUnknownSince {
		if named[queryGroup] {
			continue
		}
		if facts.Truncated && publisher.schedule != nil {
			if wake := publisher.schedule.WakeOf(queryGroup); wake.Known && wake.IntervalSeconds <= 0 {
				continue
			}
		}
		delete(publisher.periodUnknownSince, queryGroup)
	}
}

// evaluatingStrategies is every strategy seen evaluating on the given
// objects, once each, in order.
func evaluatingStrategies(owned []execution.QueryGroupIdentity, strategies func(string) []fleet.StrategyRef) []fleet.StrategyRef {
	seen := map[fleet.StrategyRef]bool{}
	list := []fleet.StrategyRef{}
	for _, queryGroup := range owned {
		for _, ref := range strategies(string(queryGroup)) {
			if !seen[ref] {
				seen[ref] = true
				list = append(list, ref)
			}
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].StrategyID != list[j].StrategyID {
			return list[i].StrategyID < list[j].StrategyID
		}
		return list[i].BusinessID < list[j].BusinessID
	})
	return list
}

// overdueEpisodeObserver logs an overdue episode's beginning and end with the
// object and its hold, and counts each beginning by its hold. An object whose
// reads are held is overdue by its hold whenever the hold is longer than its
// period -- every Slot, by design -- so that episode is INFO; one with no
// hold, or a hold not known, is overdue with nothing to explain it, at WARN.
func overdueEpisodeObserver(logger *observability.Logger, recorder *metric.Recorder) func(fleet.OverdueEpisode, bool) {
	return func(episode fleet.OverdueEpisode, began bool) {
		hold := episode.HoldClass()
		if began {
			recorder.FleetOverdueEpisodeBegan(hold)
		}
		if logger == nil {
			return
		}
		result := "ended"
		if began {
			result = "began"
		}
		write := logger.Warn
		if hold == "positive" {
			write = logger.Info
		}
		write("fleet_overdue_episode", result, 0, 0, slog.String("query_group", episode.QueryGroup),
			slog.String("hold", hold), slog.Int64("read_hold_ms", episode.ReadHoldMillis),
			slog.Time("due_at", episode.DueAt), slog.Int64("interval_seconds", episode.IntervalSeconds),
			slog.Time("onset", episode.Onset), slog.Int("strategies", len(episode.Strategies)))
	}
}

// fleetBuildFacts carries the recorder's build to the snapshot field by field,
// so the two types can differ in package without differing in content.
func fleetBuildFacts(build metric.BuildInfo) fleet.BuildFacts {
	return fleet.BuildFacts{Version: build.Version, Commit: build.Commit, SchemaVersion: build.SchemaVersion}
}

// fleetRetentionFacts is the runtime profile's retention section, for the
// fleet snapshot.
func fleetRetentionFacts(cfg config.Config) *observability.RuntimeRetentionFacts {
	facts := phaseTwoRuntimeRetention(cfg)
	return &facts
}

// fleetOutputProtocolFacts is the choice this process runs with: the word the
// reconciler was configured with -- the same call, so the fleet cannot
// publish one word while the Plans are built under another -- and whether the
// configuration spelled it. Empty in the configuration is the default, auto,
// and is published as auto with explicit false rather than as an empty word:
// an empty word is what an older build publishes by publishing nothing.
func fleetOutputProtocolFacts(cfg config.Config) *fleet.OutputProtocolFacts {
	return &fleet.OutputProtocolFacts{Configured: cfg.OutputProtocol(), Explicit: cfg.PhaseTwo.Output.Protocol != ""}
}

// buildFacts is the build this publisher was given, or nil when it was given
// none: the aggregate keeps "did not report" apart from any version, and a
// publisher that never learned its build must not publish an empty one as if
// it were a version.
func (publisher *fleetPublisher) buildFacts() *fleet.BuildFacts {
	if publisher.build == (fleet.BuildFacts{}) {
		return nil
	}
	build := publisher.build
	return &build
}

// snapshot is what this replica has to say about itself right now. It is built
// separately from being published so the two can fail independently: what the
// replica states and whether the statement reached the store are different
// questions, and a reader of either should not have to disentangle them.
func (publisher *fleetPublisher) snapshot(ctx context.Context) fleet.Snapshot {
	owned := publisher.owned()
	// One moment for the whole publish. Judging what is overdue at a different
	// instant from the one the snapshot is stamped with would have the page
	// reading two clocks as one.
	at := publisher.now()
	retained := make(map[string]struct{}, len(owned))
	for _, queryGroup := range owned {
		retained[string(queryGroup)] = struct{}{}
	}
	// Objects this replica no longer owns are dropped before the list is built,
	// so a handover cannot leave their last known state to be republished for
	// as long as the process lives.
	publisher.tracker.Forget(retained)
	for queryGroup := range publisher.restoreAttempts {
		if _, kept := retained[string(queryGroup)]; !kept {
			delete(publisher.restoreAttempts, queryGroup)
		}
	}
	publisher.restoreOwned(ctx, owned, at)
	// A retiring runner can still report while restore performs its reads.
	// Keep those observations outside this ownership snapshot as well.
	publisher.tracker.Forget(retained)

	anomalies := publisher.tracker.Anomalies()
	demoted := publisher.tracker.Demoted()
	// Overdue objects are appended to the same list rather than reported beside
	// it. They are anomalies about the same objects, and a reader looking at
	// "what is wrong right now" should not have to know that one kind of wrong
	// arrives through a different door.
	//
	// Appended once per object, though. An object the tracker already reports
	// can also be past a wake time -- during a re-warm most of them are -- and
	// adding a second row for it made the list longer than the set of objects it
	// was about. Nothing noticed while the length was only ever displayed; the
	// moment the columns were required to add up against what the replica can
	// speak for, a deployment reported 964 anomalies over 943 objects and held
	// itself at UNKNOWN. The row is the redundant one: the tracker's row for the
	// same object already says it is not progressing and carries why, while
	// "its wake time passed" is the same fact seen from the scheduler's side.
	// The count of overdue objects is unaffected -- it travels in its own facts,
	// which is why that number does not depend on this list at all.
	parked, overdue := publisherOverdue(publisher.overdue, at, publisher.replica, publisher.strategies)
	publisher.notePeriodUnknown(overdue, at)
	anomalies = append(anomalies, onlyUnlisted(parked, anomalies, demoted)...)
	awaiting, awaitingTotal := publisher.awaitingFirstRound(owned, at)
	snapshot := fleet.Snapshot{
		Replica:   publisher.replica,
		TakenAt:   at,
		StartedAt: publisher.startedAt,
		Build:     publisher.buildFacts(),
		Owned:     len(owned),
		// Read after Forget, so it counts only objects this replica still owns.
		// The difference between the two is what the replica owns but cannot
		// speak for, which the aggregate counts as unknown rather than healthy.
		Determined: publisher.tracker.Determined(),
		// The undetermined objects that are only waiting for their first
		// round, which the aggregate says as such rather than as unknown.
		AwaitingFirstRound: awaiting, AwaitingFirstRoundTotal: awaitingTotal,
		// Which objects, not only how many. A sum cannot tell two replicas
		// holding distinct shares from two replicas holding the same object,
		// and that ambiguity sat unresolved on a running deployment for over a
		// day because the sum was all anyone published.
		OwnedObjects:   ownedObjectIDs(owned),
		Anomalies:      anomalies,
		TotalAnomalies: len(anomalies),
		Overdue:        overdue,
	}
	// The pool travels beside the anomalies rather than inside them. An object
	// whose backend keeps answering unavailable is not this deployment failing,
	// and counting it as one made every backend outage read as alarmd getting
	// worse -- so the bigger the outage, the worse the verdict, which is the
	// wrong direction for the number people escalate on.
	snapshot.Demoted = demoted
	snapshot.TotalDemoted = len(demoted)
	if publisher.openAlerts != nil {
		snapshot.OpenAlertSet = publisher.openAlerts()
	}
	if publisher.controlSource != nil {
		snapshot.ControlSource = publisher.controlSource()
	}
	if publisher.platformSettings != nil {
		snapshot.PlatformSettings = publisher.platformSettings()
	}
	snapshot.MetricsUnexported = publisher.publicSurface.MetricsUnexported
	snapshot.CLIUnavailable = publisher.publicSurface.CLIUnavailable
	if publisher.activation != nil {
		snapshot.Activation = publisher.activation()
	}
	if publisher.activationHeader != nil {
		snapshot.ActivationHeader = publisher.activationHeader()
	}
	if publisher.rebalance != nil {
		snapshot.Rebalance = publisher.rebalance()
	}
	if publisher.assignmentScope != nil {
		snapshot.AssignmentScope = publisher.assignmentScope()
	}
	if publisher.assignmentSweep != nil {
		snapshot.AssignmentSweep = publisher.assignmentSweep()
	}
	if publisher.leaderRound != nil {
		snapshot.LeaderRound = publisher.leaderRound()
	}
	if publisher.viewStream != nil {
		snapshot.ViewStream = publisher.viewStream()
	}
	if publisher.source != nil {
		snapshot.Source = publisher.source()
	}
	if publisher.endpoints != nil {
		snapshot.Dependencies = publisher.endpoints()
	}
	if publisher.readiness != nil {
		snapshot.Readiness = publisher.readiness()
	}
	if publisher.declines != nil {
		snapshot.Declines = publisher.declines()
	}
	if publisher.outputProtocol != nil {
		facts := *publisher.outputProtocol
		snapshot.OutputProtocol = &facts
	}
	if publisher.retention != nil {
		facts := *publisher.retention
		snapshot.Retention = &facts
	}
	// What the tracker keeps to read holes by, and the object keeping the
	// most: the metrics carry the counts, only the snapshot can name it.
	roundMemory := publisher.tracker.RoundMemory().Summary()
	snapshot.RoundMemory = &roundMemory
	// And the objects whose rounds end without a basis to decide recovery.
	// Beside the anomalies for a different reason than the pool: not "this is
	// somebody else's fault" but "this is not a fault". Counting them as
	// anomalies described a normal condition as a standing defect, and on a
	// running deployment they were the largest population in the list.
	undecidable := publisher.tracker.Undecidable()
	snapshot.Undecidable = undecidable
	snapshot.TotalUndecidable = len(undecidable)
	// And the rounds nobody acts on because the configuration says so.
	byDesign := publisher.tracker.ByDesign()
	snapshot.ByDesign = byDesign
	snapshot.TotalByDesign = len(byDesign)
	flow := publisher.tracker.DemotionFlow()
	snapshot.DemotionEntries, snapshot.DemotionExtensions, snapshot.DemotionExits, snapshot.LastDemotionExit =
		flow.Entries, flow.Extensions, flow.Exits, flow.LastExit
	snapshot.DemotionRestored, snapshot.DemotionHandovers, snapshot.DemotionReentries =
		flow.Restored, flow.Handovers, flow.Reentries
	// The spans nothing ever evaluated. Not folded into any column: those
	// objects are running normally now, and the loss is in their past.
	snapshot.PrunedSkips = publisher.tracker.PrunedSkips()
	snapshot.GapSkips = publisher.tracker.GapSkips()
	// The strategies behind each retained record, so a row built from it can
	// be traced to something a reader can act on.
	if publisher.strategies != nil {
		for queryGroup, skip := range snapshot.PrunedSkips {
			skip.Strategies = publisher.strategies(queryGroup)
			snapshot.PrunedSkips[queryGroup] = skip
		}
		for queryGroup, skip := range snapshot.GapSkips {
			skip.Strategies = publisher.strategies(queryGroup)
			snapshot.GapSkips[queryGroup] = skip
		}
	}
	// And the objects whose data stopped: rounds completing, nothing coming
	// back. In no column, and on the data side's line.
	snapshot.NoData = publisher.tracker.NoData()
	// And the objects whose absence memory the store refuses: rounds
	// completing, results going out, and what they learn about absence not
	// written down. In no column, on its own line.
	snapshot.NoDataMemory = publisher.tracker.NoDataMemory()
	// And the objects nearing their one-object share of the retained pool:
	// rounds completing, and the next few percent of growth refused whole.
	snapshot.RetainedShare = publisher.tracker.RetainedShare()
	// And the objects the lookback found read before their data was
	// complete: rounds completing, from data that was not all there.
	if publisher.readEarly != nil {
		snapshot.ReadEarly = publisher.tracker.ReadEarly(publisher.readEarly())
	}
	if publisher.readHolds != nil {
		snapshot.ReadHolds = publisher.readHolds()
		// And the objects whose reads alarmd holds on a measured arrival
		// age: rounds completing whole, later than their time_delay says,
		// and the time_delay that would need no hold for the owner to set.
		snapshot.ReadHeld = publisher.tracker.ReadHeld(snapshot.ReadHolds)
	}
	snapshot.OverdueEpisodes = append([]fleet.OverdueEpisode(nil), publisher.overdueEpisodes...)
	// Which strategies evaluate on the objects this replica holds, for its
	// summary to count the running ones by: a strategy on no row of its is
	// running here.
	if publisher.strategies != nil {
		snapshot.EvaluatingStrategies, snapshot.EvaluatingStrategiesKnown = evaluatingStrategies(owned, publisher.strategies), true
	}
	// And the objects whose late series the lookback's supplements could
	// not recover: past their round, or the tail of a window recovered in
	// part.
	if publisher.lateSeries != nil {
		snapshot.LateSeries = publisher.tracker.LateSeries(publisher.lateSeries())
	}
	// And the problems whose objects recovered within the hour: the evidence
	// the RECOVERED reading is made of, which nothing on the current lines
	// carries once the objects have left them.
	snapshot.Recovered = publisher.tracker.Recovered()
	// And the running count of Slots executed and then closed without their
	// Progress: the records keep one span per object and cannot carry it.
	snapshot.BookkeepingAbandoned = publisher.tracker.BookkeepingAbandoned()
	// And what the no-data Plans' last deciding rounds counted, over every
	// tracked object: whether the tracking horizon is doing anything.
	snapshot.NoDataTracking = publisher.tracker.NoDataTrackingSummary()
	// Where every listed object is in its cycle, and the census over all of
	// them. From the same index and the same instant as the overdue facts, so
	// the row and the sentence above it cannot read two clocks.
	if publisher.schedule != nil {
		census := publisher.schedule.Census(at, len(owned))
		snapshot.Schedule = &census
		for _, column := range [][]fleet.Anomaly{snapshot.Anomalies, snapshot.Demoted, snapshot.Undecidable, snapshot.ByDesign, snapshot.NoData, snapshot.NoDataMemory, snapshot.RetainedShare, snapshot.ReadEarly, snapshot.ReadHeld} {
			for index := range column {
				wake := publisher.schedule.WakeOf(column[index].QueryGroup)
				column[index].Wake = &wake
				// The period beside the run of empty rounds, from the same
				// index: the number a reader holds the source's reporting
				// period against is on the row that asks the question.
				if facts := column[index].EmptyEveryRound; facts != nil {
					facts.IntervalSeconds = wake.IntervalSeconds
				}
				// What a flat short window says about itself, given the
				// period: holes sliding through with the latest they leave,
				// or the same points missing for longer than the window.
				column[index].WindowFill = fleet.WindowFillOf(column[index], wake.IntervalSeconds, at)
			}
		}
		// And the period behind each retained record, from the same index: a
		// loss in progress on a ten-second object is one mechanism, on a
		// five-minute object another, and the record alone cannot say which.
		for queryGroup, skip := range snapshot.GapSkips {
			skip.IntervalSeconds = publisher.schedule.WakeOf(queryGroup).IntervalSeconds
			snapshot.GapSkips[queryGroup] = skip
		}
		for queryGroup, skip := range snapshot.PrunedSkips {
			skip.IntervalSeconds = publisher.schedule.WakeOf(queryGroup).IntervalSeconds
			snapshot.PrunedSkips[queryGroup] = skip
		}
	}
	if publisher.capacity != nil {
		snapshot.Capacity = publisher.capacity()
	}
	if publisher.applied != nil {
		snapshot.AppliedActivationRecordRevision = publisher.applied()
	}
	if publisher.dispatch != nil {
		snapshot.Dispatch = publisher.dispatch()
	}
	return snapshot
}

// ownedObjectIDs lists what this replica holds, for the aggregate to compare
// against the catalogue and against the other replicas.
//
// Full identifiers rather than a digest: the reader's next question after "the
// two sides disagree by twelve" is "which twelve", and a hash cannot answer it.
// The cost is bounded and small -- a replica of this deployment holds a few
// hundred objects, so the list is tens of kilobytes against the snapshot's
// megabyte budget.
// awaitingFirstRound names the owned objects without a conclusion that are
// only waiting for their first round (fleet.AwaitingFirstRound), soonest due
// first and bounded, with the total. Nothing is exempted without a schedule
// to ask.
func (publisher *fleetPublisher) awaitingFirstRound(owned []execution.QueryGroupIdentity, at time.Time) ([]fleet.FirstRoundWait, int) {
	if publisher.schedule == nil {
		return nil, 0
	}
	var waits []fleet.FirstRoundWait
	for _, queryGroup := range owned {
		id := string(queryGroup)
		if publisher.tracker.HasConclusion(id) {
			continue
		}
		wake := publisher.schedule.WakeOf(id)
		if fleet.AwaitingFirstRound(wake, at) {
			waits = append(waits, fleet.FirstRoundWait{QueryGroup: id, IntervalSeconds: wake.IntervalSeconds, DueAt: wake.DueAt})
		}
	}
	total := len(waits)
	sort.Slice(waits, func(i, j int) bool {
		if !waits[i].DueAt.Equal(waits[j].DueAt) {
			return waits[i].DueAt.Before(waits[j].DueAt)
		}
		return waits[i].QueryGroup < waits[j].QueryGroup
	})
	if len(waits) > fleet.MaxFirstRoundWaits {
		waits = waits[:fleet.MaxFirstRoundWaits]
	}
	return waits, total
}

func ownedObjectIDs(owned []execution.QueryGroupIdentity) []string {
	if len(owned) == 0 {
		return nil
	}
	ids := make([]string, 0, len(owned))
	for _, queryGroup := range owned {
		ids = append(ids, string(queryGroup))
	}
	sort.Strings(ids)
	return ids
}

// onlyUnlisted keeps the anomalies whose object no column already carries.
//
// It is by object, not by row: the columns are required to partition the objects
// a replica can speak for, so one object contributing two rows breaks the
// arithmetic that makes "the rest is real problems" a statement rather than a
// hope.
func onlyUnlisted(candidates []fleet.Anomaly, listed ...[]fleet.Anomaly) []fleet.Anomaly {
	already := make(map[string]struct{})
	for _, column := range listed {
		for _, anomaly := range column {
			already[anomaly.QueryGroup] = struct{}{}
		}
	}
	kept := make([]fleet.Anomaly, 0, len(candidates))
	for _, candidate := range candidates {
		if _, duplicate := already[candidate.QueryGroup]; duplicate {
			continue
		}
		// Guards against the candidates repeating an object among themselves as
		// well, so the result is a set whichever list the duplicate came from.
		already[candidate.QueryGroup] = struct{}{}
		kept = append(kept, candidate)
	}
	return kept
}

// fleetVerdictScrapeCeiling bounds how long a scrape may wait on the control
// plane. It is a ceiling on hanging rather than a tuning knob, so it stays a
// constant instead of becoming another thing to configure.
const fleetVerdictScrapeCeiling = 5 * time.Second

// fleetVerdictSource exports the same judgment the object page shows.
//
// Two readers of one deployment must not disagree about whether anything is
// wrong. The page reads this judgment over HTTP and the host's alert rules read
// it as a metric; computing it twice would let them drift, and the drift shows
// up as "the page says fine, the alert is firing" at the worst possible moment.
//
// Cost is one read of the replicas' summaries per scrape -- the read the
// health route makes, shared with it when both come together -- against a
// summary set the size of the replica count. A replica that published no
// summary, or one without the scrape's counts, is read from its snapshot.
func fleetVerdictSource(
	service *fleet.Service,
	now func() time.Time,
	stallAfter time.Duration,
	timeout time.Duration,
) metric.FleetVerdictSource {
	return func() metric.FleetVerdict {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		// The replicas' summaries, each decided at the moment its replica
		// published it -- the read the health route makes -- so the page and
		// the alert rules read one verdict, and the breakdowns by kind,
		// failure, line and loss are the parts' counts of the same rows it was
		// settled on.
		view, part := service.Summarized(ctx, stallAfter)
		at := now()
		// A scrape decides the verdict too, and it is the one that runs
		// whether or not anybody is looking: it keeps the record current.
		service.RecordSummarizedVerdict(&view, part, at)
		return fleetVerdictOf(view, part, at)
	}
}

// fleetVerdictOf is the export of a view of the replicas' summaries and the
// part their rows add up to. The verdict and what the replicas say of
// themselves -- workers, gaps, degradations -- are the view's; what is
// counted from rows is the part's, counted on each replica at the moment it
// published (fleet.MetricRows). A part without those counts while replicas
// were counted says nothing of the rows: their families are left out rather
// than exported as none. With no replica counted there are no rows, and
// they read as none.
func fleetVerdictOf(view fleet.View, part fleet.ReplicaPart, at time.Time) metric.FleetVerdict {
	verdict := metric.FleetVerdict{
		Health: string(view.Health), Expected: view.Expected,
		Covered: view.Covered, Determined: view.Determined, Unknown: view.Unknown,
		Healthy: view.Healthy, Anomalous: view.AnomaliesTotal, Demoted: view.DemotedTotal,
		Undecidable: view.UndecidableTotal, ByDesign: view.ByDesignTotal,
	}
	verdict.Workers = []metric.FleetCount{
		{Value: "acked", Count: view.Workers.Acked},
		{Value: "lagging", Count: view.Workers.Lagging},
		{Value: "unknown", Count: view.Workers.Unknown},
	}
	gaps := newCountIndex()
	for _, gap := range view.Gaps {
		gaps.add(fleet.MetricGapKind(gap.Kind), 0)
	}
	verdict.Gaps = gaps.counts()
	replicas := map[fleet.DegradationKind]int{}
	for _, degradation := range view.Degradations {
		replicas[degradation.Kind]++
	}
	for _, kind := range fleet.DegradationKinds {
		verdict.Degradations = append(verdict.Degradations, metric.FleetCount{Value: string(kind), Count: replicas[kind]})
	}
	if objects, known := fleet.HandoverObjects(&view); known {
		verdict.HandoverObjects = &objects
	}

	if running, known := fleet.RunningStrategiesOf(&view, part); known {
		for _, word := range fleet.StateWords {
			verdict.RunningStrategies = append(verdict.RunningStrategies, metric.FleetCount{Value: string(word), Count: running[word]})
		}
	}
	rows := part.Metrics
	if rows == nil {
		if len(view.Replicas) > 0 {
			verdict.RowsUnknown = true
			return verdict
		}
		rows = &fleet.MetricRows{}
	}
	verdict.Stalled = rows.Stalled
	// Counted by closed label, never by object: a per-object series would put the
	// Query Group identity into a label and break the cardinality budget that
	// every other family here respects. The JSON API keeps reporting the real
	// value -- a response has no budget and the reader deserves the true one.
	// The parts keep kinds and failure categories as the rows carry them; they
	// are bounded to the closed labels here, as they were when this read the
	// rows, and kinds that fold into one label add up and keep the earliest
	// since. Read in a fixed order, so two scrapes of an unchanged deployment
	// export the same series in the same order.
	kinds := newCountIndex()
	for _, kind := range sortedKeys(rows.Kinds) {
		counted := rows.Kinds[kind]
		kinds.addCount(fleet.MetricKind(kind), counted.Count, at.Sub(counted.Since).Seconds())
	}
	verdict.Anomalies = kinds.counts()
	// Objects whose last round failed before it could be classified are
	// absent here rather than bucketed as "other": inventing a category for
	// them would report a cause nobody established.
	failures := newCountIndex()
	for _, category := range sortedKeys(rows.Failures) {
		failures.addCount(fleet.MetricFailureCategory(category), rows.Failures[category], 0)
	}
	verdict.Failures = failures.counts()
	// An object holding a query cooldown is in the demoted list, not the
	// anomaly list: the tracker places every object in exactly one of the two,
	// and the cooldown is what decides which. The parts count both lists.
	if view.Covered > 0 {
		cooldown := rows.Cooldown
		verdict.QueryCooldown = &cooldown
	}
	// The first screen's lines, by the count the page's lines print, and the
	// whole closed table rather than the lines that are up: a line that is
	// down is exported at zero. Absent would read the same as a build
	// without the family, and an alert on "this line is up" needs to see it
	// go down.
	lines := fleet.CheckLines(&view, part, at)
	for _, code := range fleet.Checks() {
		verdict.Checks = append(verdict.Checks, metric.FleetCount{Value: string(code), Count: lines[code]})
	}
	// The retained records by what each is, every kind at least at zero; and
	// how many recent ones were judged against no restart anchor.
	for _, loss := range fleet.Losses {
		verdict.Losses = append(verdict.Losses, metric.FleetCount{Value: string(loss), Count: rows.Losses[loss]})
	}
	verdict.Losses = append(verdict.Losses, metric.FleetCount{Value: "GRACE_UNKNOWN", Count: rows.GraceUnknown})
	return verdict
}

// sortedKeys is a map's keys in order.
func sortedKeys[K ~string, V any](values map[K]V) []K {
	keys := make([]K, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool { return keys[left] < keys[right] })
	return keys
}

// countIndex keeps first-seen order so two scrapes of an unchanged deployment
// export the same series in the same order.
type countIndex struct {
	byValue map[string]*metric.FleetCount
	order   []string
}

func newCountIndex() *countIndex {
	return &countIndex{byValue: map[string]*metric.FleetCount{}}
}

func (index *countIndex) add(value string, ageSeconds float64) {
	index.addCount(value, 1, ageSeconds)
}

// addCount adds n members, the oldest of them ageSeconds old.
func (index *countIndex) addCount(value string, n int, ageSeconds float64) {
	count, seen := index.byValue[value]
	if !seen {
		count = &metric.FleetCount{Value: value}
		index.byValue[value] = count
		index.order = append(index.order, value)
	}
	count.Count += n
	// The oldest member is what says how bad it is; the newest would hide the
	// object that has been broken since this morning.
	if ageSeconds > count.OldestAgeSeconds {
		count.OldestAgeSeconds = ageSeconds
	}
}

func (index *countIndex) counts() []metric.FleetCount {
	counts := make([]metric.FleetCount, 0, len(index.order))
	for _, value := range index.order {
		counts = append(counts, *index.byValue[value])
	}
	return counts
}

// selfMetricsRangeProvider adapts the query client the evaluation path already
// uses to the narrow shape the page needs.
//
// The scope is the one thing alarmd cannot work out for itself: which space its
// scraped metrics land in is decided outside the process. Without it the
// provider answers HTTP 200 with an empty series and SPACE_IS_NOT_EXISTS, so an
// unset scope has to refuse locally rather than travel and come back looking
// like a quiet system.
type selfMetricsRangeProvider struct {
	client   *accessuq.Client
	spaceUID string
}

func (provider selfMetricsRangeProvider) Range(
	ctx context.Context,
	promQL string,
	start, end time.Time,
	step time.Duration,
) (fleet.SeriesRange, error) {
	result, err := provider.client.Range(ctx, accessuq.RangeRequest{
		PromQL: promQL, SpaceUID: provider.spaceUID,
		Start: start, End: end, Step: step,
	})
	if err != nil {
		return fleet.SeriesRange{}, err
	}
	points := make([]fleet.SeriesPoint, 0, len(result.Points))
	for _, point := range result.Points {
		points = append(points, fleet.SeriesPoint{AtUnixMilli: point.AtUnixMilli, Value: point.Value})
	}
	return fleet.SeriesRange{
		Points: points, Code: result.Code, Message: result.Message, Partial: result.Partial,
	}, nil
}

// fleetRangeProvider returns nil when no scope is configured, which leaves the
// series route unmounted. A missing route is an honest answer; a mounted route
// that always returns nothing is not.
func fleetRangeProvider(client *accessuq.Client, spaceUID string) fleet.RangeProvider {
	if client == nil || strings.TrimSpace(spaceUID) == "" {
		return nil
	}
	return selfMetricsRangeProvider{client: client, spaceUID: strings.TrimSpace(spaceUID)}
}
