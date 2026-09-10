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
	"sort"
	"time"
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

// SinceSource records where an anomaly's start time came from, because the two
// sources do not survive the same events.
type SinceSource string

const (
	// SinceBusinessState is derived from persisted business state and
	// therefore survives a restart.
	SinceBusinessState SinceSource = "BUSINESS_STATE"
	// SinceSnapshotContinuity is only as old as the uninterrupted run of
	// snapshots that observed it, and resets when that run breaks.
	SinceSnapshotContinuity SinceSource = "SNAPSHOT_CONTINUITY"
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
	QueryGroup string      `json:"query_group"`
	Kind       string      `json:"kind"`
	ReasonCode string      `json:"reason_code,omitempty"`
	Since      time.Time   `json:"since"`
	SinceFrom  SinceSource `json:"since_from"`
	Replica    string      `json:"replica"`
	Failure    *FailureRef `json:"failure,omitempty"`
	// Stalled says the rounds have been failing to finish for longer than the
	// deployment's own budget for terminating an unfinishable Slot. The
	// distinction it draws is the one that decides whether anyone has to act: a
	// degraded round still ends and moves the cursor, while a round that never
	// ends leaves the object replaying the same evaluation forever. Both look
	// identical in a list that only shows how the last round went.
	//
	// Derived when the view is served rather than stored, so it is only as old as
	// the uninterrupted run of snapshots behind Since: it under-reports after a
	// restart rather than over-reports.
	Stalled    bool          `json:"stalled,omitempty"`
	Strategies []StrategyRef `json:"strategies,omitempty"`
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
	AnomaliesTotal int       `json:"anomalies_total"`
	Anomalies      []Anomaly `json:"anomalies"`
	Replicas       []string  `json:"replicas"`
}

// Aggregate folds the published snapshots into one view.
//
// expectation is read from the control plane rather than derived from any
// replica's own state: on a deployment with several replicas only the control
// leader knows the whole active set, and the others see an empty one. Deriving
// the denominator locally would make three replicas out of four report full
// coverage of nothing.
func Aggregate(expectation Expectation, snapshots []Snapshot, expectedReplicas []string, now time.Time, freshness time.Duration) View {
	view := View{Health: HealthHealthy, Anomalies: []Anomaly{}, Replicas: []string{}}

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
		view.Covered += snapshot.Owned
		view.Determined += snapshot.Determined
		view.AnomaliesTotal += snapshot.TotalAnomalies
		view.Anomalies = append(view.Anomalies, snapshot.Anomalies...)
		if snapshot.Truncated() {
			view.Gaps = append(view.Gaps, Gap{Kind: GapListTruncated, Replica: replica})
		}
	}

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
			view.Gaps = append(view.Gaps, Gap{Kind: GapCoverageInconsistent})
		case expected > view.Covered:
			// Added rather than assigned: objects nobody owns and objects owned
			// by a replica that cannot speak for them are both unknown, and they
			// are different objects.
			view.Unknown += expected - view.Covered
			view.Gaps = append(view.Gaps, Gap{Kind: GapOwnershipShortfall})
		}
	}

	sort.Slice(view.Anomalies, func(left, right int) bool {
		if view.Anomalies[left].Since.Equal(view.Anomalies[right].Since) {
			return view.Anomalies[left].QueryGroup < view.Anomalies[right].QueryGroup
		}
		return view.Anomalies[left].Since.Before(view.Anomalies[right].Since)
	})

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
