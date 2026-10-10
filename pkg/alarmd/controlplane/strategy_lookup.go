// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"sort"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// StrategyLookup is one strategy's standing in the catalog this process last
// published: where its Plans run, under which revisions, and every
// disposition the round recorded for it - the accepted ones and the
// withheld ones in one answer, because a strategy with several items can
// have both, and a reader asking "why is my strategy not alerting" needs
// them side by side.
//
// Answered from the Leader's memory of its last publication: no Redis read,
// no background work. A process that holds no publication of its own - a
// follower, a Leader before its first completed round, or a former Leader
// after it stepped down - answers Available false, and the caller asks the
// Leader instead.
type StrategyLookup struct {
	// Available says this process holds a publication to answer from.
	Available bool
	// Publication is the one answered from.
	Publication SnapshotPublicationRef
	// Found says the publication records the strategy at all: a Plan of it,
	// or a disposition for it. Not found is "the source never listed it",
	// which is a different answer from "listed and withheld".
	Found bool
	// Plans are the strategy's Plans in the publication, one per Query Group
	// it runs in, sorted by tenant, business, Query Group.
	Plans []StrategyPlanRef
	// Dispositions are every disposition the round recorded for the
	// strategy: ACCEPTED for the items that became Plans, and the withheld
	// ones with their reason and field path.
	Dispositions []ObjectDisposition
	// Retained says the strategy's Plans are the last good ones kept in
	// place of a document the round could not compile: the dispositions
	// the compiler produces for exactly that, STALE_CONFIG (the last good
	// Plan retained under a refusal) and PENDING_REMOVAL (kept one round
	// past its removal). A strategy with one item accepted and another
	// withheld is not retained - its Plan is the current one - so any other
	// non-accepted disposition beside a Plan says nothing about retention.
	Retained bool
}

// StrategyPlanRef is where one Plan of a strategy runs, and under what.
type StrategyPlanRef struct {
	Plan             execution.PlanIdentity
	QueryGroup       execution.QueryGroupIdentity
	ObjectDigest     execution.ObjectDigest
	SnapshotRevision execution.SnapshotRevision
	QueryRevision    execution.QueryRevision
	ScheduleRevision execution.ScheduleRevision
}

// strategyIndex is one publication indexed by strategy id. Built once per
// round from the catalog the round published - one pass over the Plans and
// one over the dispositions, no hashing - and replaced whole, so a lookup
// reads an immutable snapshot under a read lock and never sees a round half
// built. The object digest is derived at lookup time from the Query Group
// the Plan sits in, which is one marshal of one group for one question
// rather than one per group per round.
type strategyIndex struct {
	publication  SnapshotPublicationRef
	groups       []QueryGroup
	plans        map[string][]strategyPlanAt
	dispositions map[string][]ObjectDisposition
}

type strategyPlanAt struct{ group, plan int }

func buildStrategyIndex(publication SnapshotPublicationRef, groups []QueryGroup, dispositions []ObjectDisposition) *strategyIndex {
	index := &strategyIndex{publication: publication, groups: groups,
		plans: make(map[string][]strategyPlanAt, len(groups)), dispositions: make(map[string][]ObjectDisposition, len(dispositions))}
	for groupIndex := range groups {
		for planIndex := range groups[groupIndex].Plans {
			id := groups[groupIndex].Plans[planIndex].Identity.StrategyID
			index.plans[id] = append(index.plans[id], strategyPlanAt{group: groupIndex, plan: planIndex})
		}
	}
	for _, disposition := range dispositions {
		index.dispositions[disposition.SourceID] = append(index.dispositions[disposition.SourceID], disposition)
	}
	return index
}

func (index *strategyIndex) lookup(strategyID string) StrategyLookup {
	answer := StrategyLookup{Available: true, Publication: index.publication}
	if index == nil || strategyID == "" {
		return answer
	}
	for _, at := range index.plans[strategyID] {
		group := &index.groups[at.group]
		plan := &group.Plans[at.plan]
		ref := StrategyPlanRef{Plan: plan.Identity, QueryGroup: group.Identity, SnapshotRevision: index.publication.SnapshotRevision,
			QueryRevision: group.QueryPlan.QueryRevision, ScheduleRevision: group.ScheduleRevision}
		// The digest of the object this Plan is read from. It cannot fail on
		// a group that was published; if it does the ref goes out without it
		// rather than the whole answer going out empty.
		if digest, err := DeriveQueryGroupObjectDigest(*group); err == nil {
			ref.ObjectDigest = digest
		}
		answer.Plans = append(answer.Plans, ref)
	}
	sort.Slice(answer.Plans, func(left, right int) bool {
		if answer.Plans[left].Plan.TenantID != answer.Plans[right].Plan.TenantID {
			return answer.Plans[left].Plan.TenantID < answer.Plans[right].Plan.TenantID
		}
		if answer.Plans[left].Plan.BusinessID != answer.Plans[right].Plan.BusinessID {
			return answer.Plans[left].Plan.BusinessID < answer.Plans[right].Plan.BusinessID
		}
		return answer.Plans[left].QueryGroup < answer.Plans[right].QueryGroup
	})
	answer.Dispositions = append([]ObjectDisposition(nil), index.dispositions[strategyID]...)
	answer.Found = len(answer.Plans) > 0 || len(answer.Dispositions) > 0
	for _, disposition := range answer.Dispositions {
		if len(answer.Plans) > 0 && retainingDisposition(disposition.Disposition) {
			answer.Retained = true
		}
	}
	return answer
}

// retainingDisposition is a disposition under which the Plans on record are
// the last good ones and not this round's: the two the compiler produces
// when it keeps a Plan it could not rebuild.
func retainingDisposition(disposition Disposition) bool {
	return disposition == DispositionStaleConfig || disposition == DispositionPendingRemoval
}

// strategyLookupState is the reconciler's published index and its lock.
type strategyLookupState struct {
	mu    sync.RWMutex
	index *strategyIndex
}

func (state *strategyLookupState) replace(index *strategyIndex) {
	state.mu.Lock()
	state.index = index
	state.mu.Unlock()
}

func (state *strategyLookupState) lookup(strategyID string) StrategyLookup {
	state.mu.RLock()
	index := state.index
	state.mu.RUnlock()
	if index == nil {
		return StrategyLookup{}
	}
	return index.lookup(strategyID)
}

// LookupStrategy answers one strategy's standing from the catalog this
// process last published. Safe to call from any goroutine while rounds run;
// a process that holds no publication of its own answers Available false.
func (reconciler *SourceReconciler) LookupStrategy(strategyID string) StrategyLookup {
	if reconciler == nil {
		return StrategyLookup{}
	}
	return reconciler.strategies.lookup(strategyID)
}

// StepDown forgets the index: called on every tick this process runs as a
// follower, so a Leader that lost its lease stops answering from the
// publication it made in its term. Without it a former Leader kept
// answering as if it still published -- a strategy created after the
// hand-over read as "the source never listed it", from a replica no longer
// in a position to say. The next round this process completes as Leader
// builds the index again.
func (reconciler *SourceReconciler) StepDown() {
	if reconciler == nil {
		return
	}
	reconciler.strategies.replace(nil)
}
