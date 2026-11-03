package main

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/editclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// The edit close runs on the absent close's goroutine, right after each of
// its rounds, over the roster that round decided on: the strategies the
// alert link lists with an unrecovered alert. A strategy the link does not
// list has nothing to close, and one an incomplete walk did not reach is
// reached by a later one. What it closes is editclose's to decide.
//
// Each round reads at most editCloseStrategies strategies' listings and
// editCloseRecordReads alert records, starting after the last strategy a
// round finished: a strategy whose alerts the round did not get through is
// first in line next round. A decision that keeps an alert is remembered
// against the current Plans' facts, so a still-firing alert whose group
// survived the edit is read once per change of those facts, not once per
// round; a process that starts afresh reads it once more.
const (
	editCloseStrategies  = 16
	editCloseRecordReads = 500
	// editCloseRemembered bounds the decision memory. Past it the memory is
	// dropped whole and filled again from the records.
	editCloseRemembered = 100000
	editCloseAlertBatch = 64
	// editCloseRoundTime is how long one round may take: half the absent
	// close's interval, so the two together stay inside it.
	editCloseRoundTime = absentCloseInterval / 2
)

// editClosePlans is what the loop asks the control plane: the current
// Plans' facts of one strategy (controlplane.SourceReconciler).
type editClosePlans interface {
	StrategyPlanFacts(tenantID, strategyID string) (controlplane.StrategyPlanFacts, bool, bool)
}

// editCloseLink is what the loop asks the alert link: one strategy's
// active alerts and one alert's record.
type editCloseLink interface {
	Reconcile(context.Context, openalerts.StrategyKey) (openalerts.Reconciliation, error)
	AlertRecord(context.Context, string, string) (openalerts.AlertRecord, error)
}

type editAlertKey struct{ tenantID, alertID string }

// editMemory is a decision that closed nothing, and the facts it was taken
// against.
type editMemory struct {
	planKey        string
	decision, note string
}

// editStrategyClose closes the open alerts an edit of their strategy
// orphaned. See package editclose.
type editStrategyClose struct {
	bundle *phaseTwoWorkerBundle
	plans  editClosePlans
	link   editCloseLink
	writer closeWriter
	// after is the last strategy a round got through; the next round starts
	// after it.
	after absentalerts.Key
	// remembered is the decisions that closed nothing, by alert.
	remembered map[editAlertKey]editMemory
	wasLeader  bool
	countsMu   sync.Mutex
	counts     map[string]uint64
}

func newEditStrategyClose(bundle *phaseTwoWorkerBundle, plans editClosePlans, link editCloseLink, writer closeWriter) *editStrategyClose {
	return &editStrategyClose{bundle: bundle, plans: plans, link: link, writer: writer,
		remembered: make(map[editAlertKey]editMemory), counts: make(map[string]uint64)}
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

// step is one round over roster, the strategies the link lists with an
// unrecovered alert; nil is no roster read yet.
func (loop *editStrategyClose) step(ctx context.Context, roster map[absentalerts.Key]int) {
	ctx, cancel := context.WithTimeout(ctx, editCloseRoundTime)
	defer cancel()
	ctx = openalerts.WithRequestCoverage(ctx, editCloseRoundTime)
	if !loop.leading() {
		if loop.wasLeader {
			loop.remembered = make(map[editAlertKey]editMemory)
			loop.after = absentalerts.Key{}
		}
		loop.wasLeader = false
		loop.count(editclose.OutcomeNotLeader, 1)
		return
	}
	loop.wasLeader = true
	if roster == nil {
		loop.observe(ctx, editclose.OutcomeRosterUnavailable, nil, 1)
		return
	}
	keys := make([]absentalerts.Key, 0, len(roster))
	for key := range roster {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return absentalerts.LessKey(keys[i], keys[j]) })
	// Rotated: the strategies after the last one a round got through first.
	start := sort.Search(len(keys), func(i int) bool { return absentalerts.LessKey(loop.after, keys[i]) })
	keys = append(keys[start:], keys[:start]...)
	reads := editCloseRecordReads
	now := loop.bundle.dependencies.Now()
	examined := 0
	for _, key := range keys {
		if examined >= editCloseStrategies || ctx.Err() != nil || !loop.leading() {
			break
		}
		facts, available, found := loop.plans.StrategyPlanFacts(key.TenantID, key.StrategyID)
		if !available {
			loop.observe(ctx, editclose.OutcomePublicationUnavailable, nil, 1)
			return
		}
		if word := strategyRefusal(facts, found); word != "" {
			// Nothing to read: decided from memory, and does not use up the
			// round's listings.
			loop.count(word, 1)
			loop.after = key
			continue
		}
		examined++
		if !loop.closeStrategy(ctx, key, planOf(facts), &reads, now) {
			// Out of reads: this strategy is first next round.
			return
		}
		loop.after = key
	}
}

// strategyRefusal is why a listed strategy is not examined, or empty.
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

// closeStrategy decides and closes one strategy's listed alerts, spending
// the round's record reads. It reports whether it got through every listed
// alert; false is the reads running out (or the round ending) first.
func (loop *editStrategyClose) closeStrategy(ctx context.Context, key absentalerts.Key, plan editclose.Plan, reads *int, now time.Time) bool {
	strategyID, err := strconv.ParseInt(key.StrategyID, 10, 64)
	if err != nil || strategyID <= 0 {
		loop.count(editclose.OutcomePlanUnrevisioned, 1)
		return true
	}
	reconciliation, err := loop.link.Reconcile(ctx, openalerts.StrategyKey{TenantID: key.TenantID, StrategyID: key.StrategyID})
	if err != nil {
		// This strategy waits for a later round; the others go on.
		loop.observe(ctx, editclose.OutcomeEvidenceUnavailable, err, 1)
		return true
	}
	if reconciliation.Truncated {
		// The listed alerts are decided; the rest wait for a later round.
		loop.count(editclose.OutcomeEvidenceTruncated, 1)
	}
	planKey := plan.Key()
	closes := make([]linkdoutput.CloseRequest, 0)
	unreadable, deferred, unknownBusiness := 0, 0, 0
	complete := true
	for index, alert := range reconciliation.Alerts {
		switch alert.EventSourceID {
		case reconciliation.EventSourceID:
		case "":
			loop.count(editclose.OutcomeProducerUnknown, 1)
			continue
		default:
			loop.count(editclose.OutcomeProducerForeign, 1)
			continue
		}
		memoryKey := editAlertKey{tenantID: key.TenantID, alertID: alert.AlertID}
		if memory, ok := loop.remembered[memoryKey]; ok && memory.planKey == planKey {
			loop.count(memory.decision, 1)
			loop.count(memory.note, 1)
			loop.count(editclose.OutcomeRemembered, 1)
			continue
		}
		if *reads <= 0 || ctx.Err() != nil {
			deferred = countOwn(reconciliation.Alerts[index:], reconciliation.EventSourceID)
			complete = false
			break
		}
		*reads--
		record, err := loop.link.AlertRecord(ctx, key.TenantID, alert.AlertID)
		if err != nil || record.EventSourceID != reconciliation.EventSourceID || record.StrategyID != key.StrategyID {
			// Not decided on a record that is not this alert's: it stays
			// open and is read again on a later round.
			unreadable++
			continue
		}
		decision, note := editclose.Decide(plan, editclose.Alert{Revision: record.Revision, DimensionKeys: record.DimensionKeys,
			DimensionsRead: record.DimensionsRead, EvaluationFamily: record.EvaluationFamily, Severity: record.Severity,
			Facts: record.StrategyFacts})
		loop.count(decision, 1)
		loop.count(note, 1)
		if !editclose.Closes(decision) {
			loop.remember(memoryKey, editMemory{planKey: planKey, decision: decision, note: note})
			continue
		}
		if record.BusinessID == 0 {
			unknownBusiness++
			continue
		}
		closes = append(closes, linkdoutput.CloseRequest{TenantID: key.TenantID, Fingerprint: alert.Fingerprint,
			AlertInstanceID: alert.AlertID, StrategyID: strategyID, StrategyRevision: plan.Revision, BusinessID: record.BusinessID,
			OccurredAt: now, Reason: linkdoutput.CloseReasonStrategyChanged, Detail: decision})
	}
	if unreadable > 0 {
		loop.observe(ctx, editclose.OutcomeRecordUnreadable, errors.New("an alert's record could not be read as this source's alert of this strategy"), unreadable)
	}
	if unknownBusiness > 0 {
		loop.observe(ctx, editclose.OutcomeBusinessUnknown, errors.New("an alert's record names no business to file its close under"), unknownBusiness)
	}
	loop.count(editclose.OutcomeReadDeferred, deferred)
	for start := 0; start < len(closes); start += editCloseAlertBatch {
		batch := closes[start:min(len(closes), start+editCloseAlertBatch)]
		if err := loop.writer.WriteCloseBatch(ctx, batch); err != nil {
			// Still open: a later round decides them again.
			loop.observe(ctx, editclose.OutcomeSendFailed, err, len(closes)-start)
			break
		}
		loop.count(editclose.OutcomeCloseSent, len(batch))
	}
	if len(closes) > 0 {
		loop.bundle.dependencies.Observer.Observe(ctx, observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageStrategyEditClose,
			Result: observability.ResultSuccess, Counts: observability.Counts{Events: int64(len(closes))}})
	}
	return complete
}

func countOwn(alerts []openalerts.Alert, source string) int {
	n := 0
	for _, alert := range alerts {
		if alert.EventSourceID == source {
			n++
		}
	}
	return n
}

// remember keeps a decision that closed nothing; a full memory is dropped
// whole first, and counted.
func (loop *editStrategyClose) remember(key editAlertKey, memory editMemory) {
	if len(loop.remembered) >= editCloseRemembered {
		loop.remembered = make(map[editAlertKey]editMemory)
		loop.count(editclose.OutcomeMemoryFull, 1)
	}
	loop.remembered[key] = memory
}

// observe counts an outcome and writes its line.
func (loop *editStrategyClose) observe(ctx context.Context, outcome string, err error, count int) {
	loop.count(outcome, count)
	loop.bundle.dependencies.Observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageStrategyEditClose,
		Result: observability.ResultDegraded, ReasonCode: observability.ReasonCode(outcome),
		Counts: observability.Counts{Events: int64(count)}, Err: err})
}
