// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package fleet aggregates what each replica knows about the objects it owns
// into one answer about the whole deployment.
//
// The aggregation exists to make one failure impossible: a replica that stops
// publishing takes its anomalies with it, the remaining list gets shorter, and
// a naive reading calls that an improvement. Every gap therefore has to be
// counted as unknown, and unknown is never healthy.
package fleet

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Health is the answer to "does someone need to look at this".
type Health string

const (
	// HealthUnknown means the view is incomplete. It is not a degraded state
	// and it is not a healthy one; it means the question cannot be answered.
	HealthUnknown Health = "UNKNOWN"
	// HealthDegraded means the view is complete and contains anomalies.
	HealthDegraded Health = "DEGRADED"
	// HealthHealthy means the view is complete and contains no anomalies.
	HealthHealthy Health = "HEALTHY"
)

// GapKind classifies why part of the deployment is unaccounted for.
type GapKind string

const (
	// GapDenominatorUnavailable means the expected object set could not be
	// read. Without it there is no way to know what is missing.
	GapDenominatorUnavailable GapKind = "DENOMINATOR_UNAVAILABLE"
	// GapReplicaMissing means a replica that should have published a snapshot
	// did not.
	GapReplicaMissing GapKind = "REPLICA_MISSING"
	// GapSnapshotStale means a snapshot is older than the freshness budget, so
	// the objects it reports may no longer be the objects that exist.
	GapSnapshotStale GapKind = "SNAPSHOT_STALE"
	// GapListTruncated means a replica had more anomalies than it could
	// publish, so the list is bounded rather than complete.
	GapListTruncated GapKind = "LIST_TRUNCATED"
	// GapOwnershipShortfall means the covered objects do not add up to the
	// expected set even though every snapshot looked fresh.
	GapOwnershipShortfall GapKind = "OWNERSHIP_SHORTFALL"
	// GapCoverageInconsistent means more objects were reported as covered than
	// the expected set contains, so the two sides disagree and neither can be
	// trusted as the denominator.
	GapCoverageInconsistent GapKind = "COVERAGE_INCONSISTENT"
	// GapNoReplicas means nothing was expected to publish. A deployment with no
	// ready replica is not a healthy deployment with nothing to do.
	GapNoReplicas GapKind = "NO_REPLICAS"
	// GapUndetermined means a replica owns objects it cannot yet speak for, so
	// their absence from the anomaly list is not evidence that they are well.
	GapUndetermined GapKind = "UNDETERMINED"
)

// SinceSource records where an anomaly's start time came from, because the
// sources do not survive the same events and do not mean the same thing.
//
// A restored object is the reason this has to be carried per anomaly rather
// than stamped on the whole list: an object this process watched go wrong has a
// start time, and an object restored from a persisted cursor has a bound. Both
// render as a timestamp, and nothing else in the row tells them apart.
type SinceSource string

const (
	// SinceBusinessState is derived from persisted business state and
	// therefore survives a restart.
	SinceBusinessState SinceSource = "BUSINESS_STATE"
	// SinceSnapshotContinuity is only as old as the uninterrupted run of
	// snapshots that observed it, and resets when that run breaks.
	SinceSnapshotContinuity SinceSource = "SNAPSHOT_CONTINUITY"
	// SinceRestoredLastFull is the last round the object is known to have
	// completed in full, read back after a restart. It is not when the object
	// started going wrong: that moment is not persisted anywhere. It is the
	// latest moment the object is known to have been fine, so the duration
	// beside it is an upper bound on how long this has been going on.
	SinceRestoredLastFull SinceSource = "RESTORED_LAST_FULL"
	// SinceRestoredAtRestart is this process taking over an object whose
	// persisted state records no full completion to anchor against. The clock
	// starts at the handover, so the duration is a lower bound -- possibly a
	// far lower one -- rather than a measurement.
	SinceRestoredAtRestart SinceSource = "RESTORED_AT_RESTART"
	// SinceRefusedFuture marks a row whose start time was later than the moment
	// it was read. Nothing can have started after now, so the timestamp was
	// refused and the clock reset to the read.
	//
	// This should never appear. It exists because the previous way of getting it
	// wrong was silent: a future timestamp renders as a negative age, and the
	// list is ordered oldest-first, so it sorted to the end -- where a truncated
	// list drops it first. The rule meant to keep the worst objects visible
	// pushed the mis-stamped ones out of view instead. A row that says it was
	// refused is a bug report; a row that quietly sorts last is not.
	SinceRefusedFuture SinceSource = "REFUSED_FUTURE"
)

// The two columns the object list can be about. An object is in exactly one.
const (
	// ColumnAnomalies is what this deployment's own execution is failing at.
	ColumnAnomalies = "anomalies"
	// ColumnDemoted is what it has stopped asking for because the backend kept
	// answering unavailable. These are held out of the health verdict, which is
	// the whole reason they have to be listable: a mechanism that removes
	// objects from the denominator has to show which ones.
	ColumnDemoted = "demoted"
)

// StrategyRef ties an object back to something an operator recognises.
type StrategyRef struct {
	StrategyID string `json:"strategy_id"`
	BusinessID string `json:"business_id"`
}

// FailureRef is why the object's last failing round failed, as the bounded
// classification the pipeline already emits.
//
// The completion kind says a round ended with something unavailable; it does not
// say what. Without this, a page can list a hundred objects sharing one reason
// code and still leave the reader with no idea what to look at.
type FailureRef struct {
	Stage    string `json:"stage"`
	Category string `json:"category"`
	Code     string `json:"code,omitempty"`
}

// Anomaly is one object that is not making progress as expected.
type Anomaly struct {
	QueryCooldown *observability.QueryCooldownFacts `json:"query_cooldown,omitempty"`
	QueryGroup    string                            `json:"query_group"`
	Kind          string                            `json:"kind"`
	ReasonCode    string                            `json:"reason_code,omitempty"`
	// Cause separates the conditions that share one completion kind. Without it
	// a page can list hundreds of objects as degraded and give the reader no
	// way to tell which ones anyone can do something about, which is the same
	// as listing none.
	Cause     string      `json:"cause,omitempty"`
	Since     time.Time   `json:"since"`
	SinceFrom SinceSource `json:"since_from"`
	// FailingSince is when the current unbroken sequence of rounds that
	// reached execution and did not finish began; zero while the last
	// conclusive round ended, however it ended. It is not Since: an object
	// can have been degraded for hours and failing to finish for a minute,
	// and Stalled is judged against this clock, not that one, precisely so a
	// long degraded history cannot turn one retrying round into a stall.
	//
	// A zero value is left off the wire by MarshalJSON below rather than by an
	// omitzero tag: the release pipeline builds with Go 1.23, whose encoder
	// does not know that option and would print a zero time on every object
	// whose rounds are ending normally.
	FailingSince time.Time   `json:"failing_since"`
	Replica      string      `json:"replica"`
	Failure      *FailureRef `json:"failure,omitempty"`
	// Stalled says the rounds have been failing to finish for longer than the
	// deployment's own budget for terminating an unfinishable Slot. The
	// distinction it draws is the one that decides whether anyone has to act: a
	// degraded round still ends and moves the cursor, while a round that never
	// ends leaves the object replaying the same evaluation forever. Both look
	// identical in a list that only shows how the last round went.
	//
	// Derived when the view is served rather than stored, so it is only as old as
	// the uninterrupted run of snapshots behind FailingSince: it under-reports
	// after a restart rather than over-reports.
	Stalled    bool          `json:"stalled,omitempty"`
	Strategies []StrategyRef `json:"strategies,omitempty"`
}

// MarshalJSON leaves failing_since off the wire while it is zero. The outer
// field shadows the embedded one of the same name, so a nil pointer is
// omitted and a set one carries the time; nothing else about the encoding
// changes, and decoding needs no counterpart because a missing field decodes
// to the zero time.
func (anomaly Anomaly) MarshalJSON() ([]byte, error) {
	type wire Anomaly
	encoded := struct {
		wire
		FailingSince *time.Time `json:"failing_since,omitempty"`
	}{wire: wire(anomaly)}
	if !anomaly.FailingSince.IsZero() {
		encoded.FailingSince = &anomaly.FailingSince
	}
	return json.Marshal(encoded)
}

// Snapshot is one replica's contribution. Owned is the number of objects the
// replica holds, which is what makes the coverage arithmetic possible: the
// anomaly list alone cannot distinguish "nothing wrong" from "nothing seen".
//
// Determined completes that arithmetic. Owning an object is not the same as
// being able to speak for it: a replica that just restarted owns everything and
// knows nothing, and an empty anomaly list from it is indistinguishable from a
// healthy one unless the two counts are reported separately.
type Snapshot struct {
	Replica        string    `json:"replica"`
	TakenAt        time.Time `json:"taken_at"`
	Owned          int       `json:"owned"`
	Determined     int       `json:"determined"`
	Anomalies      []Anomaly `json:"anomalies"`
	TotalAnomalies int       `json:"total_anomalies"`
	// Demoted are the objects held back because their backend kept answering
	// unavailable. They are published apart from Anomalies, not folded into
	// them, because they answer a different question: Anomalies is what this
	// deployment is failing at, Demoted is what it has stopped asking for.
	Demoted      []Anomaly `json:"demoted"`
	TotalDemoted int       `json:"total_demoted"`
	// DemotionEntries and DemotionExits are cumulative since this replica
	// started. The pair is the check on the pool: demotion removes objects from
	// the health denominator, so a pool with entries and no exits is a mechanism
	// that only hides, and its occupancy alone cannot show that.
	DemotionEntries    int `json:"demotion_entries"`
	DemotionExtensions int `json:"demotion_extensions"`
	DemotionExits      int `json:"demotion_exits"`
	// LastDemotionExit is when this replica last saw an object leave the pool.
	// Zero means it has not seen one, which is not the same as "none left
	// recently" and must not be rendered as a duration.
	LastDemotionExit time.Time `json:"last_demotion_exit,omitempty"`
	// Capacity is how close this replica is to its own limits. Absent on a
	// replica that does not report it, which is why the aggregate counts the
	// replicas it actually heard from rather than assuming every one answered.
	Capacity *Capacity `json:"capacity,omitempty"`
	// Overdue counts the objects this replica should have picked up and has not.
	// Absent means the replica has nothing holding wake times, which is a
	// different answer from "nothing is overdue" and must not be shown as one.
	Overdue *OverdueFacts `json:"overdue,omitempty"`
	// Suppression is present only on a build whose dispatcher actually holds
	// objects back. Absent, the overdue count above is structurally zero and
	// must not be read as "nothing is overdue".
	//
	// Pointer and omitempty are both load-bearing, and the guard for that is
	// TestSnapshotOmitsDispatchOnAReplicaThatDoesNotSuppress rather than a note
	// here: it asserts on the encoded form, which is where the page reads this.
	Dispatch *DispatchSuppression `json:"dispatch,omitempty"`
}

// Truncated reports whether the replica had more anomalies than it published.
func (s Snapshot) Truncated() bool {
	return s.TotalAnomalies > len(s.Anomalies)
}

// Gap is one reason the view is incomplete.
type Gap struct {
	Kind    GapKind `json:"kind"`
	Replica string  `json:"replica,omitempty"`
	Detail  string  `json:"detail,omitempty"`
}

// Expectation is the denominator. Known is false when the control plane object
// could not be read, which is different from an empty deployment.
type Expectation struct {
	QueryGroups int
	Known       bool
}

// View is the aggregated answer returned to callers.
type View struct {
	Health   Health `json:"health"`
	Expected *int   `json:"expected"`
	Covered  int    `json:"covered"`
	// Determined is the subset of Covered whose owning replica has actually
	// observed a conclusive round. Covered minus Determined is counted into
	// Unknown, not into health.
	Determined int   `json:"determined"`
	Unknown    int   `json:"unknown"`
	Gaps       []Gap `json:"gaps,omitempty"`
	// AnomaliesTotal is how many anomalies the replicas actually had, which is
	// larger than the returned list whenever a snapshot was truncated. Reporting
	// the returned length as the total would understate an incident by exactly
	// the amount that made it worth reporting.
	AnomaliesTotal int `json:"anomalies_total"`
	// Capacity answers "how close is this deployment to its limits" from the
	// same read that produced the verdict, so the two cannot disagree and the
	// answer does not wait on collection.
	Capacity *CapacityView `json:"capacity,omitempty"`
	// Overdue counts objects nothing came back for, across the deployment. The
	// objects themselves are in the anomaly list; this survives that list being
	// paged or truncated, because "how many objects are not being evaluated" is
	// the one number that must not depend on how much of the list fitted.
	Overdue *OverdueFacts `json:"overdue,omitempty"`
	// Suppression says whether anything in this deployment can be parked at
	// all. See DispatchSuppression: its absence, not its value, is the answer.
	Dispatch  *DispatchSuppression `json:"dispatch,omitempty"`
	Anomalies []Anomaly            `json:"anomalies"`
	// Healthy is Determined minus the two listed columns. It is computed here
	// rather than left to the page because it is the column a reader trusts
	// most, and a page that derives it by subtraction can be made to show a
	// healthy count that nothing produced.
	Healthy int `json:"healthy"`
	// Demoted are objects whose backend kept answering unavailable. They are
	// out of the health verdict on purpose -- that is what demotion is for --
	// which is exactly why the flow numbers below travel with them.
	Demoted      []Anomaly `json:"demoted"`
	DemotedTotal int       `json:"demoted_total"`
	// DemotionEntries, DemotionExits and LastDemotionExit are summed over the
	// counted replicas. Exits is the one that matters: demotion takes objects
	// out of the denominator, so a pool that fills and never drains is a
	// mechanism for making a deployment look well, and only this number says so.
	DemotionEntries    int       `json:"demotion_entries"`
	DemotionExtensions int       `json:"demotion_extensions"`
	DemotionExits      int       `json:"demotion_exits"`
	LastDemotionExit   time.Time `json:"last_demotion_exit,omitempty"`
	// DemotedDue counts pooled objects whose own cooldown window has already
	// elapsed at the moment of this read: they are due to be tried again and are
	// still in the pool.
	//
	// It is derived from each object's own deadline rather than from a threshold
	// chosen here, which is why it can be reported before anyone has decided
	// what number is too many. A pool where this keeps climbing is one whose way
	// out has stopped working, and that is the failure demotion can cause and
	// occupancy cannot show.
	//
	// No alert threshold is set on any of this, and that is a decision rather
	// than an omission. Drawing the line needs a deployment's own numbers, and
	// the four published here are what it needs: entries and exits give the
	// balance, extensions separate a pool being retried from one nobody is
	// touching, and this one says how many are already overdue for that retry.
	// Anyone can draw the line from two reads of these; nobody can draw a
	// defensible one today, and a number invented now would be obeyed later as
	// though it had been measured.
	DemotedDue int      `json:"demoted_due"`
	Replicas   []string `json:"replicas"`
}

// Aggregate folds the published snapshots into one view.
//
// expectation is read from the control plane rather than derived from any
// replica's own state: on a deployment with several replicas only the control
// leader knows the whole active set, and the others see an empty one. Deriving
// the denominator locally would make three replicas out of four report full
// coverage of nothing.
func Aggregate(expectation Expectation, snapshots []Snapshot, expectedReplicas []string, now time.Time, freshness time.Duration) View {
	view := View{Health: HealthHealthy, Anomalies: []Anomaly{}, Demoted: []Anomaly{}, Replicas: []string{}}
	ownedByReplica := make([]string, 0, len(expectedReplicas))
	// The snapshots this view is willing to speak for. Every other number below
	// is built from these and not from the argument, because the argument
	// contains snapshots that were rejected: one too old to describe the
	// deployment now, and one from a replica that is no longer part of it and
	// whose key has simply not expired yet.
	counted := make([]Snapshot, 0, len(expectedReplicas))

	byReplica := make(map[string]Snapshot, len(snapshots))
	for _, snapshot := range snapshots {
		byReplica[snapshot.Replica] = snapshot
	}

	// No replica means no evidence. Reporting healthy here would turn the whole
	// deployment being gone into the quietest possible answer.
	if len(expectedReplicas) == 0 {
		view.Gaps = append(view.Gaps, Gap{Kind: GapNoReplicas})
	}

	for _, replica := range expectedReplicas {
		snapshot, published := byReplica[replica]
		if !published {
			view.Gaps = append(view.Gaps, Gap{Kind: GapReplicaMissing, Replica: replica})
			continue
		}
		if freshness > 0 && now.Sub(snapshot.TakenAt) > freshness {
			view.Gaps = append(view.Gaps, Gap{Kind: GapSnapshotStale, Replica: replica})
			continue
		}
		view.Replicas = append(view.Replicas, replica)
		counted = append(counted, snapshot)
		ownedByReplica = append(ownedByReplica, fmt.Sprintf("%s %d", shortReplicaName(replica), snapshot.Owned))
		view.Covered += snapshot.Owned
		view.Determined += snapshot.Determined
		view.AnomaliesTotal += snapshot.TotalAnomalies
		view.Anomalies = append(view.Anomalies, snapshot.Anomalies...)
		view.DemotedTotal += snapshot.TotalDemoted
		view.Demoted = append(view.Demoted, snapshot.Demoted...)
		view.DemotionEntries += snapshot.DemotionEntries
		view.DemotionExtensions += snapshot.DemotionExtensions
		view.DemotionExits += snapshot.DemotionExits
		if snapshot.LastDemotionExit.After(view.LastDemotionExit) {
			view.LastDemotionExit = snapshot.LastDemotionExit
		}
		if snapshot.Truncated() {
			view.Gaps = append(view.Gaps, Gap{Kind: GapListTruncated, Replica: replica})
		}
	}
	// Determined is what the replicas can speak for, and every object they can
	// speak for is in exactly one of the three. Subtracting rather than counting
	// healthy objects directly is deliberate: a healthy count built by its own
	// walk can drift from the lists beside it, and the drift shows up as a
	// column that adds up to slightly more than the deployment has.
	view.Healthy = view.Determined - view.AnomaliesTotal - view.DemotedTotal
	if view.Healthy < 0 {
		// The three columns claim more objects than the replicas said they can
		// speak for. Something is being counted twice, so no column can be
		// trusted -- including the healthy one.
		//
		// The difference goes to UNKNOWN and nowhere else, and this looks like
		// needless caution until you ask where else it could go. Letting the
		// healthy column absorb it just makes that number smaller, and a smaller
		// healthy count is the one reading nobody investigates: it looks like
		// the deployment having a bad day, which is exactly what a page full of
		// anomalies has already told them. A double count that lands there is
		// never found. Every other column is looked at by someone who wants it
		// explained, so an error that lands in one of those surfaces on its own.
		//
		// So this is not "be conservative when unsure". It is: the arithmetic
		// broke, and the only honest place to put a broken number is the column
		// that means "do not trust this view".
		view.Gaps = append(view.Gaps, Gap{Kind: GapCoverageInconsistent,
			Detail: fmt.Sprintf("%d anomalies and %d demoted against %d determined; the columns claim more "+
				"objects than the replicas can speak for, so none of them adds up",
				view.AnomaliesTotal, view.DemotedTotal, view.Determined)})
		view.Healthy = 0
	}

	// Built from the same snapshots as the verdict, for the same reason the
	// verdict refuses them: a stale snapshot's occupancy is what the process was
	// doing when it last published, and the page presents capacity as "right
	// now". Reading the whole argument here meant a deployment whose replicas had
	// all gone quiet showed UNKNOWN with no live replica beside memory and permit
	// figures from before it went quiet -- the one moment those numbers are read
	// hardest, and the one moment they are not about the present.
	aggregateCapacity(&view, counted)
	// Same snapshots, same reason: a stale replica's idea of what it has not
	// picked up describes a moment that has passed.
	aggregateOverdue(&view, counted)
	aggregateDispatchSuppression(&view, counted)

	// Owning an object is not knowing about it. A replica that has just restarted
	// owns everything and has observed nothing, so its empty anomaly list is not
	// evidence of health -- and neither is the list of an object whose every
	// round is inconclusive, nor of one the tracker dropped at its bound.
	switch {
	case view.Determined > view.Covered:
		view.Gaps = append(view.Gaps, Gap{Kind: GapCoverageInconsistent,
			Detail: "more objects reported as determined than owned"})
	case view.Covered > view.Determined:
		view.Unknown += view.Covered - view.Determined
		view.Gaps = append(view.Gaps, Gap{Kind: GapUndetermined})
	}

	if !expectation.Known {
		view.Gaps = append(view.Gaps, Gap{Kind: GapDenominatorUnavailable})
	} else {
		expected := expectation.QueryGroups
		view.Expected = &expected
		switch {
		case view.Covered > expected:
			// Counting is not set arithmetic: more covered than expected means
			// the two sides disagree about which objects exist, and a shortfall
			// could still be hiding inside that difference.
			//
			// It says both numbers because this gap alone holds the verdict at
			// UNKNOWN. Naming only the condition leaves a reader with "do not
			// trust this" and nothing to act on -- they cannot tell twelve
			// objects left over from a catalogue that shrank from a deployment
			// owning hundreds it should not, and those call for opposite
			// responses. The sibling branch above already explains itself; this
			// one did not, which is the whole difference.
			// The per-replica split travels with the totals because Covered is
			// a sum, and a sum cannot tell two replicas owning distinct shares
			// from two replicas both holding the same objects through a
			// rendezvous change. Those are different problems -- one is stale
			// ownership to clean up, the other is this view double counting --
			// and stating only the difference asserts the first.
			view.Gaps = append(view.Gaps, Gap{Kind: GapCoverageInconsistent,
				Detail: fmt.Sprintf("%d owned (%s) against %d expected, %d more; Covered is a sum, so replicas "+
					"holding the same object during a rendezvous change look the same as objects left over",
					view.Covered, strings.Join(ownedByReplica, ", "), expected, view.Covered-expected)})
		case expected > view.Covered:
			// Added rather than assigned: objects nobody owns and objects owned
			// by a replica that cannot speak for them are both unknown, and they
			// are different objects.
			view.Unknown += expected - view.Covered
			view.Gaps = append(view.Gaps, Gap{Kind: GapOwnershipShortfall})
		}
	}

	sortAnomalies(view.Anomalies)
	sortAnomalies(view.Demoted)
	for _, demoted := range view.Demoted {
		if demoted.QueryCooldown != nil && !demoted.QueryCooldown.Until.IsZero() &&
			demoted.QueryCooldown.Until.Before(now) {
			view.DemotedDue++
		}
	}

	// Order matters: an incomplete view cannot be called healthy, and it cannot
	// be called degraded either, because the anomalies it does show are not the
	// whole story.
	switch {
	case len(view.Gaps) > 0:
		view.Health = HealthUnknown
	case len(view.Anomalies) > 0:
		view.Health = HealthDegraded
	default:
		view.Health = HealthHealthy
	}
	return view
}

// sortAnomalies orders a column oldest-first. Both columns use it, because a
// reader comparing them is comparing two lists and an ordering that differs
// between them turns "which has been wrong longer" into a question about the
// page.
func sortAnomalies(anomalies []Anomaly) {
	sort.Slice(anomalies, func(left, right int) bool {
		if anomalies[left].Since.Equal(anomalies[right].Since) {
			return anomalies[left].QueryGroup < anomalies[right].QueryGroup
		}
		return anomalies[left].Since.Before(anomalies[right].Since)
	})
}

// shortReplicaName keeps the part of a Pod name that differs between replicas.
// The full name repeats the deployment on every entry, which pushes the numbers
// this detail exists for off the end of whatever is reading it.
func shortReplicaName(replica string) string {
	if index := strings.LastIndex(replica, "-"); index >= 0 && index+1 < len(replica) {
		return replica[index+1:]
	}
	return replica
}
