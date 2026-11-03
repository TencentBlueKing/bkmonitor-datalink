package main

import (
	"context"
	"sort"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/editclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The edit close runs on the absent close's goroutine, right after each of
// its rounds, over the strategies that round's snapshot holds, through the
// link's search and close by filter (filterclose.Closer.Edit). What it
// closes is editclose's to decide.
//
// A strategy is examined until one round finds no alert of it opened at
// another version than its current one; it is then remembered at that
// revision and passed over until its revision changes, or the term ends.
// A strategy whose older alerts are kept (their group survived the edit)
// is examined every round. Each round examines at most editFilterStrategies
// strategies, starting after the last one a round got to.
const (
	// editFilterStrategies is the round's strategies: a strategy costs one
	// search when no older version is open, a few more when one is.
	editFilterStrategies = 256
	// editCloseRoundTime is how long one round may take: half the absent
	// close's interval, so the two together stay inside it.
	editCloseRoundTime = absentCloseInterval / 2
)

// editClosePlans is what the loop asks the control plane: the current
// Plans' facts of one strategy (controlplane.SourceReconciler).
type editClosePlans interface {
	StrategyPlanFacts(tenantID, strategyID string) (controlplane.StrategyPlanFacts, bool, bool)
}

// editStrategyClose closes the open alerts an edit of their strategy
// orphaned. See package editclose.
type editStrategyClose struct {
	bundle *phaseTwoWorkerBundle
	plans  editClosePlans
	filter *filterRoute
	// after is the last strategy a round got to; the next round starts
	// after it.
	after absentalerts.Key
	// clean is, per strategy, the revision a round found nothing of
	// another version at.
	clean     map[absentalerts.Key]int64
	wasLeader bool
	countsMu  sync.Mutex
	counts    map[string]uint64
}

func newEditStrategyClose(bundle *phaseTwoWorkerBundle, plans editClosePlans, filter *filterRoute) *editStrategyClose {
	return &editStrategyClose{bundle: bundle, plans: plans, filter: filter, clean: map[absentalerts.Key]int64{}, counts: make(map[string]uint64)}
}

// Stats is every outcome's count.
func (loop *editStrategyClose) Stats() map[string]uint64 {
	loop.countsMu.Lock()
	defer loop.countsMu.Unlock()
	counts := make(map[string]uint64, len(editclose.Outcomes))
	for _, outcome := range editclose.Outcomes {
		counts[outcome] = loop.counts[outcome]
	}
	return counts
}

func (loop *editStrategyClose) count(outcome string, n int) {
	if n <= 0 || outcome == "" {
		return
	}
	loop.countsMu.Lock()
	loop.counts[outcome] += uint64(n)
	loop.countsMu.Unlock()
}

// leading is the absent close's: control leader, neither draining nor
// closed.
func (loop *editStrategyClose) leading() bool {
	loop.bundle.mu.RLock()
	defer loop.bundle.mu.RUnlock()
	return loop.bundle.controlLeader && !loop.bundle.draining && !loop.bundle.closed
}

// step is one round over the snapshot's strategies; nil is no snapshot
// read yet.
func (loop *editStrategyClose) step(ctx context.Context, snapshot map[absentalerts.Key]struct{}) {
	ctx, cancel := context.WithTimeout(ctx, editCloseRoundTime)
	defer cancel()
	if !loop.leading() {
		if loop.wasLeader {
			loop.clean = map[absentalerts.Key]int64{}
			loop.after = absentalerts.Key{}
		}
		loop.wasLeader = false
		loop.count(editclose.OutcomeNotLeader, 1)
		return
	}
	loop.wasLeader = true
	if snapshot == nil {
		loop.observe(ctx, editclose.OutcomeRosterUnavailable, nil, 1)
		return
	}
	if loop.filter == nil {
		loop.observe(ctx, editclose.OutcomeEvidenceUnavailable, nil, 1)
		return
	}
	for key := range loop.clean {
		if _, held := snapshot[key]; !held {
			delete(loop.clean, key)
		}
	}
	keys := make([]absentalerts.Key, 0, len(snapshot))
	for key := range snapshot {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return absentalerts.LessKey(keys[i], keys[j]) })
	// Rotated: the strategies after the last one a round got to first.
	start := sort.Search(len(keys), func(i int) bool { return absentalerts.LessKey(loop.after, keys[i]) })
	keys = append(keys[start:], keys[:start]...)
	examined := 0
	for _, key := range keys {
		if examined >= editFilterStrategies || ctx.Err() != nil || !loop.leading() {
			break
		}
		facts, available, found := loop.plans.StrategyPlanFacts(key.TenantID, key.StrategyID)
		if !available {
			loop.observe(ctx, editclose.OutcomePublicationUnavailable, nil, 1)
			return
		}
		if word := strategyRefusal(facts, found); word != "" {
			// Nothing to read: decided from memory, and does not use up the
			// round's searches.
			loop.count(word, 1)
			loop.after = key
			continue
		}
		if loop.clean[key] == facts.Revision {
			loop.count(editclose.OutcomeRemembered, 1)
			loop.after = key
			continue
		}
		examined++
		// Each bucket's expectation is alarmd's own record of the strategy's
		// open episodes at its version (filterclose.Closer.Edit).
		word, buckets := loop.filter.closer.Edit(ctx, filterclose.EditInput{Scope: loop.filter.scope(key), Plan: planOf(facts),
			Period: facts.Interval})
		if word == filterclose.OutcomeUnsupported {
			return
		}
		if word == "" && buckets == 0 {
			loop.clean[key] = facts.Revision
		}
		loop.after = key
	}
}

// strategyRefusal is why a strategy is not examined, or empty.
func strategyRefusal(facts controlplane.StrategyPlanFacts, found bool) string {
	switch {
	case !found:
		return editclose.OutcomeStrategyUnpublished
	case facts.Revision <= 0 || !facts.HasOutputIdentity:
		return editclose.OutcomePlanUnrevisioned
	case facts.Retained:
		return editclose.OutcomePlanRetained
	case !facts.Agree:
		return editclose.OutcomePlansDisagree
	}
	return ""
}

func planOf(facts controlplane.StrategyPlanFacts) editclose.Plan {
	return editclose.Plan{Revision: facts.Revision, DimensionFields: facts.DimensionFields, DynamicDimensions: facts.DynamicDimensions,
		RecordFields: facts.RecordDimensionFields,
		Levels:       facts.Levels, LevelsWhole: facts.LevelsWhole,
		NoData: facts.NoData, NoDataDimensions: facts.NoDataAggDimension, NoDataSuspended: facts.NoDataSuspended,
		Facts: facts.StrategyFacts}
}

// observe counts an outcome and writes its line.
func (loop *editStrategyClose) observe(ctx context.Context, outcome string, err error, count int) {
	loop.count(outcome, count)
	loop.bundle.dependencies.Observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageStrategyEditClose,
		Result: observability.ResultDegraded, ReasonCode: observability.ReasonCode(outcome),
		Counts: observability.Counts{Events: int64(count)}, Err: err})
}
