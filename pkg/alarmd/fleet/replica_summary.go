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

// ReplicaSummary is what a replica publishes beside its snapshot for the
// reads that want its numbers and not its rows: the snapshot without its
// rows, the part its rows add up to, and the objects it holds as a digest.
// The health route reads one summary per replica instead of every snapshot.
type ReplicaSummary struct {
	// Head is the snapshot with its rows and its owned objects left out: the
	// replica's own facts, which Aggregate reads the same from either.
	Head Snapshot `json:"head"`
	// Part is what the rows add up to, decided at the snapshot's TakenAt.
	Part ReplicaPart `json:"part"`
	// Owned is the digest of every object the replica holds: the whole set,
	// not the snapshot's list cut to its budget.
	Owned SetDigest `json:"owned"`
	// AnomaliesCut is whether the snapshot's anomaly list was cut to its
	// budget, which the head has no list left to tell by.
	AnomaliesCut bool `json:"anomalies_cut,omitempty"`

	// owned is the owned list of a summary the reader made from a snapshot,
	// which is where its coverage is read from; fromSnapshot says it was.
	owned        []string
	fromSnapshot bool
}

// SummaryOf is the summary of a snapshot as published -- its rows cut to
// the budget -- with owned the whole owned set before the cut. The rows are
// decided at the snapshot's TakenAt by the rule the health route decides a
// view by, so a summary the replica makes and one the reader makes from the
// same snapshot are the same, but for the running strategies: only the
// replica's own snapshot says which strategies evaluate, as that list is not
// written (Snapshot.EvaluatingStrategies).
func SummaryOf(snapshot Snapshot, owned []string, stallAfter time.Duration) ReplicaSummary {
	view := publishedView(snapshot, stallAfter)
	part := ReplicaPartOf(view, snapshot.TakenAt)
	if snapshot.EvaluatingStrategiesKnown {
		part.RunningStrategies = runningStrategiesOf(view, snapshot.EvaluatingStrategies, snapshot.TakenAt)
	}
	return ReplicaSummary{Head: headOf(snapshot), Part: part, Owned: DigestOf(owned), AnomaliesCut: snapshot.Truncated()}
}

// publishedView is the view of one snapshot with its rows decided at its
// own TakenAt: the moment the replica published them, and the one rule its
// summary and a reader of its rows both decide them by.
func publishedView(snapshot Snapshot, stallAfter time.Duration) View {
	view := Aggregate(Expectation{}, []Snapshot{snapshot}, []string{snapshot.Replica}, snapshot.TakenAt, 0)
	Decide(&view, snapshot.TakenAt, stallAfter)
	return view
}

// summaryFromSnapshot is the summary a reader makes for a replica that
// published none -- an older build during a rollout -- from its snapshot,
// keeping the owned list the snapshot carries for coverage.
func summaryFromSnapshot(snapshot Snapshot, stallAfter time.Duration) ReplicaSummary {
	summary := SummaryOf(snapshot, snapshot.OwnedObjects, stallAfter)
	summary.owned, summary.fromSnapshot = snapshot.OwnedObjects, true
	return summary
}

// headOf is the snapshot without the lists it carries a row or an object
// per entry of (snapshotRowFields).
func headOf(snapshot Snapshot) Snapshot {
	head := snapshot
	head.OwnedObjects = nil
	head.Anomalies, head.Demoted, head.Undecidable, head.ByDesign = nil, nil, nil, nil
	head.PrunedSkips, head.GapSkips = nil, nil
	head.NoData, head.NoDataMemory, head.RetainedShare, head.ReadEarly, head.ReadHeld, head.LateSeries = nil, nil, nil, nil, nil, nil
	head.ReadHolds, head.OverdueEpisodes, head.EvaluatingStrategies = nil, nil, nil
	return head
}

// AggregateSummaries is Aggregate over replicas' summaries: the view of
// their heads, with what Aggregate reads from rows taken from their parts
// merged over the replicas the view counts, and that merged part.
//
// Coverage is decided by digests first: the counted replicas' digests added
// up equal to expected, the digest of the objects expected, is no object
// held by several, held unexpected, or expected unheld. Otherwise ownedSets
// is asked for the counted replicas' owned lists, and whether each is the
// whole set, and the lists name the objects as Aggregate names them.
func AggregateSummaries(expectation Expectation, expected SetDigest, summaries []ReplicaSummary, expectedReplicas []string,
	now time.Time, freshness time.Duration, ownedSets func(replicas []string) ([][]string, bool)) (View, ReplicaPart) {
	heads := make([]Snapshot, 0, len(summaries))
	byReplica := make(map[string]*ReplicaSummary, len(summaries))
	facts := &headFacts{cut: make(map[string]bool, len(summaries))}
	for index := range summaries {
		summary := &summaries[index]
		heads = append(heads, summary.Head)
		byReplica[summary.Head.Replica] = summary
		facts.cut[summary.Head.Replica] = summary.AnomaliesCut
	}
	facts.coverage = func(counted []string) *Disagreement {
		held := SetDigest{}
		for _, replica := range counted {
			held = held.Add(byReplica[replica].Owned)
		}
		if expectation.Known && len(expectation.IDs) > 0 && held == expected {
			return &Disagreement{Comparable: true, setsWhole: true}
		}
		sets, whole := ownedSets(counted)
		if coverage := compareCoverage(sets, expectation, whole); coverage != nil || whole {
			return coverage
		}
		// Something the digests cannot place, and the lists that would name
		// it not all read: not comparable, rather than nothing to say -- the
		// counts beside it may be a handover's.
		return &Disagreement{}
	}
	view := aggregate(expectation, heads, expectedReplicas, now, freshness, facts)
	parts := make([]ReplicaPart, 0, len(view.Replicas))
	tallies := make(map[string]AttributionTally, len(view.Replicas))
	for _, replica := range view.Replicas {
		part := byReplica[replica].Part
		parts = append(parts, part)
		tallies[replica] = part.Attribution
	}
	merged := MergeReplicaParts(parts...)
	view.DemotedDue, view.DemotedDueOldestSeconds = merged.DemotedDue, merged.DemotedDueOldestSeconds(now)
	view.EmptyEveryRoundTotal = merged.EmptyEveryRound
	settleFrom(&view, tallies, merged.Attribution)
	return view, merged
}
