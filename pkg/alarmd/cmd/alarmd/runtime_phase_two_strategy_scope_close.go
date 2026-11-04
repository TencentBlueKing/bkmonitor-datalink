package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// The strategy scope close runs on the absent close's goroutine, after the
// edit close, on the control leader. On the leader's start and on each
// change of a strategy's scope it sends one filter close per strategy: the
// strategy's active alerts of this source that carry its target's
// dimensions and are of none of its current targets
// (filterclose.Closer.ScopeChange), checked by a dry run against alarmd's
// own record of the strategy's episodes outside those targets.
//
// It is what closes an alert whose target left the scope, whether or not
// the target still reports.
//
// Only a resolution that is Complete, with its exclusions resolved, drives
// a close: anything less would read a missing member as a removed one.
// "Scope change" is the digest of the current member set differing from
// the last one this leader term closed for; a term starts with none, so a
// new leader sends once per strategy. A strategy with no target plan (the
// whole business) is not this close's.
const (
	// scopeCloseStrategies bounds the strategies a round sends for; the
	// rest wait for the next round. Each costs a dry run and the closes.
	scopeCloseStrategies = 64
	// scopeRefusedRounds is how many rounds a strategy whose close was
	// refused past its record waits before it is tried again.
	scopeRefusedRounds = 3
)

// The loop's outcomes, closed: a metric label is made of them.
const (
	scopeCloseNotLeader              = "not_leader"
	scopeClosePublicationUnavailable = "publication_unavailable"
	scopeCloseSnapshotUnavailable    = "snapshot_unavailable"
	scopeCloseLinkUnavailable        = "link_unavailable"
	scopeClosePlansDisagree          = "plans_disagree"
	// scopeCloseResolutionIncomplete is a strategy whose target did not
	// resolve Complete with its exclusions: skipped, asked again next
	// round.
	scopeCloseResolutionIncomplete = "resolution_incomplete"
	// scopeCloseResolutionStale is a strategy whose target resolved from a
	// snapshot kept past its refresh (Resolution.StaleAge): a host that
	// joined the scope since is missing from it, and a close against it
	// would close that host's alerts. Skipped, not taken as a read of the
	// scope, asked again next round.
	scopeCloseResolutionStale  = "resolution_stale"
	scopeCloseMemberUnreadable = "member_unreadable"
	// scopeCloseEmpty is a strategy whose target resolved Complete with no
	// member: nothing is closed. A filter with no current target would
	// select every alert of the strategy that carries the target's
	// dimensions, and the record does not stop it (each entry with a
	// target is outside an empty scope, so the expectation covers them
	// all).
	scopeCloseEmpty = "scope_empty"
	// scopeCloseUnconfirmed is a scope read once: it closes only when the
	// next round reads the same one, as the worker side closes only on a
	// second rejected Slot.
	scopeCloseUnconfirmed = "scope_unconfirmed"
	scopeCloseUnchanged   = "scope_unchanged"
	// scopeCloseBackoff is a strategy whose last close was refused past its
	// record (expect_exceeded) waiting out scopeRefusedRounds before the
	// next try: a transient under-count (a record write still queued)
	// heals, a lasting one stays counted in linkd_filter_close_total.
	scopeCloseBackoff   = "refused_backoff"
	scopeCloseRoundFull = "round_full"
	// scopeCloseSent is a strategy whose close was run; its word is
	// filterclose's (linkd_filter_close_total).
	scopeCloseSent = "close_run"
	// scopeCloseSettled is a run whose word settles the scope: the next
	// round sends nothing until it changes.
	scopeCloseSettled = "scope_settled"
)

var scopeCloseOutcomes = []string{scopeCloseNotLeader, scopeClosePublicationUnavailable, scopeCloseSnapshotUnavailable,
	scopeCloseLinkUnavailable, scopeClosePlansDisagree, scopeCloseResolutionIncomplete, scopeCloseResolutionStale,
	scopeCloseMemberUnreadable, scopeCloseEmpty, scopeCloseUnconfirmed, scopeCloseUnchanged, scopeCloseBackoff, scopeCloseRoundFull, scopeCloseSent, scopeCloseSettled}

// strategyTargets is what the loop asks the control plane
// (controlplane.SourceReconciler).
type strategyTargets interface {
	StrategyTarget(tenantID, strategyID string) (controlplane.StrategyTarget, bool, bool)
}

// strategyScopeClose is the loop's state.
type strategyScopeClose struct {
	bundle   *phaseTwoWorkerBundle
	plans    strategyTargets
	resolver worker.TargetResolver
	filter   *filterRoute
	// closed is, per strategy, the scope digest this term closed for;
	// seen the digest the last round read, waiting for its second read.
	closed map[absentalerts.Key]string
	seen   map[absentalerts.Key]string
	// refused is, per strategy whose last close was refused past its
	// record, the rounds left before the next try.
	refused   map[absentalerts.Key]int
	wasLeader bool
	countsMu  sync.Mutex
	counts    map[string]uint64
}

func newStrategyScopeClose(bundle *phaseTwoWorkerBundle, plans strategyTargets, resolver worker.TargetResolver,
	filter *filterRoute) *strategyScopeClose {
	return &strategyScopeClose{bundle: bundle, plans: plans, resolver: resolver, filter: filter,
		closed: map[absentalerts.Key]string{}, seen: map[absentalerts.Key]string{}, refused: map[absentalerts.Key]int{},
		counts: map[string]uint64{}}
}

// Stats is every outcome's count.
func (loop *strategyScopeClose) Stats() map[string]uint64 {
	loop.countsMu.Lock()
	defer loop.countsMu.Unlock()
	counts := make(map[string]uint64, len(scopeCloseOutcomes))
	for _, outcome := range scopeCloseOutcomes {
		counts[outcome] = loop.counts[outcome]
	}
	return counts
}

func (loop *strategyScopeClose) count(outcome string, n int) {
	if n <= 0 {
		return
	}
	loop.countsMu.Lock()
	loop.counts[outcome] += uint64(n)
	loop.countsMu.Unlock()
}

func (loop *strategyScopeClose) leading() bool {
	loop.bundle.mu.RLock()
	defer loop.bundle.mu.RUnlock()
	return loop.bundle.controlLeader && !loop.bundle.draining && !loop.bundle.closed
}

// settles says a part's word ends that part of the scope's decision:
// closed, nothing to close, refused by the request's bounds. A word of a
// failed or unfinished request, or job_done_rederived (items left for the
// filter to find again), leaves it to the next round; expect_exceeded to a
// round after scopeRefusedRounds. The decision ends only when every part
// does (settled): a part a filter could not be built for ends itself, not
// a part beside it with more to close.
func settles(word string) bool {
	switch word {
	case filterclose.OutcomeJobDone, filterclose.OutcomeNothingMatched, filterclose.OutcomeTargetListTooLarge,
		filterclose.OutcomeFilterRefused:
		return true
	}
	return false
}

// settled says every part of a scope close ended (settles). A part ending
// filter_refused beside another part is a defence: such a part needs a
// filter alarmd cannot build from inputs it built the other part's filter
// from, which no input reaches today, so judging on the word to wait on
// instead (settles(result.Word)) answers the same for every decision a
// test can make; the rule is held on its parts
// (TestAScopeClosePartSettlesOnlyItself).
func settled(result filterclose.Result) bool {
	for _, word := range result.Parts {
		if !settles(word) {
			return false
		}
	}
	return true
}

// refusedPastRecord says a scope close waits scopeRefusedRounds: a part
// was refused past its record (expect_exceeded) and no part has more to
// close, which the next round goes on with instead.
func refusedPastRecord(result filterclose.Result) bool {
	if result.Again() {
		return false
	}
	for _, word := range result.Parts {
		if word == filterclose.OutcomeExpectExceeded {
			return true
		}
	}
	return false
}

// step is one round over the snapshot's strategies; nil is no snapshot
// read yet.
func (loop *strategyScopeClose) step(ctx context.Context, snapshot map[absentalerts.Key]struct{}) {
	ctx, cancel := context.WithTimeout(ctx, editCloseRoundTime)
	defer cancel()
	if !loop.leading() {
		if loop.wasLeader {
			loop.closed, loop.seen, loop.refused = map[absentalerts.Key]string{}, map[absentalerts.Key]string{}, map[absentalerts.Key]int{}
		}
		loop.wasLeader = false
		loop.count(scopeCloseNotLeader, 1)
		return
	}
	loop.wasLeader = true
	switch {
	case snapshot == nil:
		loop.observe(ctx, scopeCloseSnapshotUnavailable, "", 1)
		return
	case loop.filter == nil || loop.resolver == nil:
		loop.observe(ctx, scopeCloseLinkUnavailable, "", 1)
		return
	}
	for key := range loop.closed {
		if _, held := snapshot[key]; !held {
			delete(loop.closed, key)
		}
	}
	for key := range loop.seen {
		if _, held := snapshot[key]; !held {
			delete(loop.seen, key)
		}
	}
	for key := range loop.refused {
		if _, held := snapshot[key]; !held {
			delete(loop.refused, key)
		}
	}
	keys := make([]absentalerts.Key, 0, len(snapshot))
	for key := range snapshot {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return absentalerts.LessKey(keys[i], keys[j]) })
	sent := 0
	for _, key := range keys {
		if ctx.Err() != nil || !loop.leading() {
			return
		}
		target, available, found := loop.plans.StrategyTarget(key.TenantID, key.StrategyID)
		if !available {
			loop.observe(ctx, scopeClosePublicationUnavailable, "", 1)
			return
		}
		if !found || target.Plan == nil {
			continue
		}
		if !target.Agree {
			loop.count(scopeClosePlansDisagree, 1)
			continue
		}
		resolution := loop.resolver.ResolveFor(ctx, key.TenantID, target.Plan, target.Interval)
		if resolution == nil || resolution.State != targetplan.ResolutionComplete || resolution.ExclusionUnavailable {
			loop.count(scopeCloseResolutionIncomplete, 1)
			continue
		}
		if resolution.StaleAge != 0 {
			loop.count(scopeCloseResolutionStale, 1)
			continue
		}
		identity := target.Plan.Identity
		current, ok := currentTargets(identity, resolution.Members())
		if !ok {
			loop.observe(ctx, scopeCloseMemberUnreadable, key.StrategyID, 1)
			continue
		}
		if len(current) == 0 {
			delete(loop.seen, key)
			loop.observe(ctx, scopeCloseEmpty, key.StrategyID, 1)
			continue
		}
		digest := scopeDigest(identity, resolution.Members())
		if loop.closed[key] == digest {
			loop.count(scopeCloseUnchanged, 1)
			continue
		}
		// The second read is the resolver's next answer, a round later, not
		// a second source: the resolver answers from the CMDB cache's
		// snapshots (cmdbcache, rebuilt every RefreshInterval), so where no
		// rebuild falls between the two rounds both reads are one snapshot.
		// The confirmation catches a scope that moves between rounds (a
		// rebuild mid-change, a partial read); a wrong snapshot that stays
		// two rounds is closed against. seen is not keyed on the
		// snapshot's revision: the resolution carries none.
		if loop.seen[key] != digest {
			loop.seen[key] = digest
			loop.count(scopeCloseUnconfirmed, 1)
			continue
		}
		if wait := loop.refused[key]; wait > 0 {
			loop.refused[key] = wait - 1
			loop.count(scopeCloseBackoff, 1)
			continue
		}
		if sent >= scopeCloseStrategies {
			loop.count(scopeCloseRoundFull, 1)
			continue
		}
		sent++
		shape := filterclose.TargetShape{Dimensions: identity.Dimensions, ModelDimension: identity.ModelDimension, ModelValue: identity.ModelValue}
		started := time.Now()
		result := loop.filter.closer.ScopeChangeResult(ctx, loop.filter.scope(key), shape, current, target.Revision, digest, target.Interval)
		word := result.Word
		observeCloseJob(ctx, loop.bundle.dependencies.Observer, observability.StageStrategyScopeClose, scopeCloseSent, key.StrategyID, word,
			result.Sent, time.Since(started))
		loop.count(scopeCloseSent, 1)
		if word == filterclose.OutcomeTargetListTooLarge {
			loop.observe(ctx, filterclose.OutcomeTargetListTooLarge, key.StrategyID, 0)
		}
		if word == filterclose.OutcomeUnsupported {
			return
		}
		if refusedPastRecord(result) {
			loop.refused[key] = scopeRefusedRounds
		}
		if settled(result) {
			loop.closed[key] = digest
			loop.count(scopeCloseSettled, 1)
		}
	}
}

// currentTargets decodes the member keys into the dimensions the alerts
// carry: each key's parts, in the identity's order, one per dimension
// (contract.TargetPlanIdentityV1.Key spells a record's key the same way).
// False when a key does not split into exactly that many parts.
func currentTargets(identity contract.TargetPlanIdentityV1, members []string) ([]filterclose.Target, bool) {
	if len(identity.Dimensions) == 0 {
		return nil, false
	}
	targets := make([]filterclose.Target, 0, len(members))
	for _, member := range members {
		parts := strings.Split(member, contract.TargetPlanKeySeparator)
		if len(parts) != len(identity.Dimensions) {
			return nil, false
		}
		target := make(filterclose.Target, len(parts))
		for i, name := range identity.Dimensions {
			if parts[i] == "" {
				return nil, false
			}
			target[name] = parts[i]
		}
		targets = append(targets, target)
	}
	return targets, true
}

// scopeDigest names a scope: the identity's shape and the sorted members.
func scopeDigest(identity contract.TargetPlanIdentityV1, members []string) string {
	h := sha256.New()
	for _, part := range append(append([]string{strconv.Itoa(len(identity.Dimensions))}, identity.Dimensions...),
		identity.ModelDimension, identity.ModelValue) {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	for _, member := range members {
		h.Write([]byte(member))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// observe counts an outcome and writes its line.
func (loop *strategyScopeClose) observe(ctx context.Context, outcome, strategyID string, count int) {
	loop.count(outcome, count)
	if loop.bundle.dependencies.Observer == nil {
		return
	}
	ctx = observability.ContextWithTraceFields(ctx, observability.TraceFields{StrategyID: strategyID})
	loop.bundle.dependencies.Observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageStrategyScopeClose,
		Result: observability.ResultDegraded, ReasonCode: observability.ReasonCode(outcome),
		Counts: observability.Counts{Events: int64(max(count, 1))}})
}
