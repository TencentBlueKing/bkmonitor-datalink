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
	"strings"
	"time"

	accessuq "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
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
	activation, err := source.repository.LoadActivation(ctx)
	if err != nil {
		return fleet.Expectation{}, err
	}
	groups, err := source.repository.LoadActiveQueryGroupSet(ctx, activation.ActiveQGSetRef)
	if err != nil {
		return fleet.Expectation{}, err
	}
	return fleet.Expectation{QueryGroups: len(groups), Known: true}, nil
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
	workers, err := source.store.ListReadyWorkers(ctx, at)
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
	owned   func() []execution.QueryGroupIdentity
	now     func() time.Time
	// observe reports each publish outcome. A failure is retried on the next
	// tick rather than propagated: the snapshot is diagnostics, and diagnostics
	// must not be able to stop the pipeline whose facts they describe.
	observe func(error)
	// restore reads an owned object's persisted Progress so a replica that has
	// just started can speak for it without waiting to watch a fresh round.
	// Nil disables it, and the replica then reports every object as unknown
	// until each completes one -- the behaviour a restart used to force.
	restore func(context.Context, execution.QueryGroupIdentity) (fleet.RestoredState, bool)
	// staleAfter is how far behind an object's Progress cursor may be before
	// its persisted completion stops being evidence about now.
	staleAfter time.Duration
	// restoreBudget bounds how many objects one publish may restore. Reading
	// every owned object at once would turn every restart into a burst against
	// the control plane at exactly the moment the process is least settled;
	// spreading it over the publish ticks costs a few more seconds of unknown
	// and no burst at all.
	restoreBudget int
	// capacity reports this replica's own limits and how much of them is in
	// use, so the page can answer "how close are we" from the same read that
	// produced the verdict instead of waiting on collection.
	capacity func() *fleet.Capacity
	// overdue is the scheduler's due index, asked which owned objects are past
	// a wake time nothing corrected. Nil on a deployment with no index, and the
	// snapshot then carries no overdue facts at all -- which is a different
	// answer from "none are overdue" and has to stay one.
	overdue fleet.OverdueWakeSource
	// strategies names the strategies behind a Query Group, so an overdue
	// object arrives in the list identified the way every other anomaly is. A
	// row nobody can trace back to a strategy is a row nobody can act on.
	strategies func(string) []fleet.StrategyRef
	// restored names objects already considered, so an object whose Progress
	// says nothing is not re-read on every tick forever.
	restored map[execution.QueryGroupIdentity]struct{}
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

// restoreOwned seeds objects this replica owns but has not yet watched.
//
// It runs before the snapshot is built, so the first publish after a restart
// already carries what the control plane knew, instead of reporting the whole
// deployment as unknown until the slowest object comes round again.
func (publisher *fleetPublisher) restoreOwned(ctx context.Context, owned []execution.QueryGroupIdentity, at time.Time) {
	if publisher.restore == nil || publisher.restoreBudget <= 0 {
		return
	}
	if publisher.restored == nil {
		publisher.restored = make(map[execution.QueryGroupIdentity]struct{}, len(owned))
	}
	spent := 0
	for _, queryGroup := range owned {
		if spent >= publisher.restoreBudget {
			return
		}
		if _, considered := publisher.restored[queryGroup]; considered {
			continue
		}
		if publisher.tracker.Tracked() > 0 && publisher.tracker.HasObserved(string(queryGroup)) {
			publisher.restored[queryGroup] = struct{}{}
			continue
		}
		spent++
		publisher.restored[queryGroup] = struct{}{}
		state, ok := publisher.restore(ctx, queryGroup)
		if !ok {
			continue
		}
		publisher.tracker.Restore(string(queryGroup), state, at, publisher.staleAfter)
	}
}

func (publisher *fleetPublisher) publishOnce(ctx context.Context) {
	owned := publisher.owned()
	// One moment for the whole publish. Judging what is overdue at a different
	// instant from the one the snapshot is stamped with would have the page
	// reading two clocks as one.
	at := publisher.now()
	publisher.restoreOwned(ctx, owned, at)
	retained := make(map[string]struct{}, len(owned))
	for _, queryGroup := range owned {
		retained[string(queryGroup)] = struct{}{}
	}
	// Objects this replica no longer owns are dropped before the list is built,
	// so a handover cannot leave their last known state to be republished for
	// as long as the process lives.
	publisher.tracker.Forget(retained)

	anomalies := publisher.tracker.Anomalies()
	// Overdue objects are appended to the same list rather than reported beside
	// it. They are anomalies about the same objects, and a reader looking at
	// "what is wrong right now" should not have to know that one kind of wrong
	// arrives through a different door.
	parked, overdue := publisherOverdue(publisher.overdue, at, publisher.replica, publisher.strategies)
	anomalies = append(anomalies, parked...)
	snapshot := fleet.Snapshot{
		Replica: publisher.replica,
		TakenAt: at,
		Owned:   len(owned),
		// Read after Forget, so it counts only objects this replica still owns.
		// The difference between the two is what the replica owns but cannot
		// speak for, which the aggregate counts as unknown rather than healthy.
		Determined:     publisher.tracker.Determined(),
		Anomalies:      anomalies,
		TotalAnomalies: len(anomalies),
		Overdue:        overdue,
	}
	if publisher.capacity != nil {
		snapshot.Capacity = publisher.capacity()
	}
	// The outcome is reported either way, including success. Reporting only
	// failures would leave the observer unable to tell recovery from silence,
	// and silence is exactly what a broken publisher produces.
	err := publisher.store.Publish(ctx, snapshot)
	if publisher.observe != nil {
		publisher.observe(err)
	}
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
// Cost is one control plane read per scrape -- the same read the object API
// already performs, against a snapshot set the size of the replica count.
func fleetVerdictSource(
	service *fleet.Service,
	now func() time.Time,
	stallAfter time.Duration,
	timeout time.Duration,
) metric.FleetVerdictSource {
	return func() metric.FleetVerdict {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		at := now()
		view := service.View(ctx)
		fleet.MarkStalled(view.Anomalies, at, stallAfter)
		return fleetVerdictOf(view, at)
	}
}

func fleetVerdictOf(view fleet.View, at time.Time) metric.FleetVerdict {
	verdict := metric.FleetVerdict{
		Health: string(view.Health), Expected: view.Expected,
		Covered: view.Covered, Determined: view.Determined, Unknown: view.Unknown,
	}
	// Counted by closed label, never by object: a per-object series would put the
	// Query Group identity into a label and break the cardinality budget that
	// every other family here respects. The JSON API keeps reporting the real
	// value -- a response has no budget and the reader deserves the true one.
	kinds := newCountIndex()
	failures := newCountIndex()
	for _, anomaly := range view.Anomalies {
		if anomaly.Stalled {
			verdict.Stalled++
		}
		kinds.add(fleet.MetricKind(anomaly.Kind), at.Sub(anomaly.Since).Seconds())
		// Objects whose last round failed before it could be classified are
		// absent here rather than bucketed as "other": inventing a category for
		// them would report a cause nobody established.
		if anomaly.Failure != nil && anomaly.Failure.Category != "" {
			failures.add(fleet.MetricFailureCategory(anomaly.Failure.Category), 0)
		}
	}
	verdict.Anomalies = kinds.counts()
	verdict.Failures = failures.counts()

	gaps := newCountIndex()
	for _, gap := range view.Gaps {
		gaps.add(fleet.MetricGapKind(gap.Kind), 0)
	}
	verdict.Gaps = gaps.counts()
	return verdict
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
	count, seen := index.byValue[value]
	if !seen {
		count = &metric.FleetCount{Value: value}
		index.byValue[value] = count
		index.order = append(index.order, value)
	}
	count.Count++
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
