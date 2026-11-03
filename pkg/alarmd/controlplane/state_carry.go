// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// State carry outcomes, one per activation whose state generation moved.
const (
	StateCarryCarried            = "carried"
	StateCarryPartial            = "partial"
	StateCarryDetectChanged      = "none_detect_changed"
	StateCarryPreviousUnreadable = "none_previous_unreadable"
	StateCarryDiscontinuous      = "none_discontinuous"
)

type changedPlan struct {
	plan    FrozenPlan
	group   execution.QueryGroupIdentity
	dataset contract.DatasetContractV2
}

// observeStateCarry reports one activation's carry outcome.
func (reconciler *ScheduleActivationReconciler) observeStateCarry(ctx context.Context, outcome string) {
	reconciler.repository.observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageStateCarryDecided,
		Result:     observability.ResultSuccess,
		StateCarry: &observability.StateCarryFacts{Scope: "plan", Result: outcome, Count: 1},
	})
}

// applyStateCarries puts the decided carries on their records. A record whose
// Query Group is back from retirement carries nothing: there is a hole between
// the old state and now. Every other carried record carries, and warms up
// behind its guard for one full Slot rather than for its whole window, while
// that Slot writes the carried history under the new generation; a series the
// previous generation held nothing for is short of points and stays WARMING on
// its own window, as a new series does.
func applyStateCarries(records []PlanActivationRecord, carries map[int]*execution.StateCarry, returned map[int]struct{}) (carried, discontinuous int) {
	for index, carry := range carries {
		if _, back := returned[index]; back {
			discontinuous++
			continue
		}
		records[index].Fact.Selected.Carry = carry
		records[index].Fact.Selected.RequiredFullSlots = 1
		carried++
	}
	return carried, discontinuous
}

// applyNoDataCarries puts the decided no-data carries on their records,
// except on a record whose Query Group is back from retirement: the hole
// between the old state and now is the Query Group's whole absence, so
// nothing is carried across it.
func applyNoDataCarries(records []PlanActivationRecord, carries map[int]*execution.NoDataCarry, returned map[int]struct{}) {
	for index, carry := range carries {
		if _, back := returned[index]; !back {
			records[index].Fact.Selected.NoDataCarry = carry
		}
	}
}

// StateCarryOutcomes are the outcomes in the order a reader groups them.
var StateCarryOutcomes = []string{StateCarryCarried, StateCarryPartial, StateCarryDetectChanged,
	StateCarryPreviousUnreadable, StateCarryDiscontinuous}

// stateCarry decides what an activation whose state generation moved while
// its Plan stayed active may carry over: the Levels present in both versions
// of the Plan whose detect fingerprints are the same. Their stored results are
// the same facts under the new generation - the trigger and the recovery are
// summarized from those results every time, so a changed trigger or recovery
// reads the old results the way it would have read them all along.
//
// The previous version is read back by the content digest the previous
// activation ran and compiled again. When it cannot be, nothing is carried
// and the Plan warms up whole, as it did before any of this. What the Levels
// decide is only where the warming guard goes: the Worker keeps a carried
// point only when its own detect fingerprint is the new one, so a Level named
// here whose points were fingerprinted another way ends up with no history
// and warms up on its window rather than on a guard.
func (reconciler *ScheduleActivationReconciler) stateCarry(
	ctx context.Context,
	previous PlanActivationRecord,
	previousContent activatedContent,
	current changedPlan,
) (*execution.StateCarry, string) {
	previousPlan, previousDataset, _, err := reconciler.previousPlan(ctx, previous, previousContent, current.group)
	if err != nil {
		return nil, StateCarryPreviousUnreadable
	}
	before, err := reconciler.levelDetectFingerprints(ctx, previousPlan, previousDataset)
	if err != nil {
		return nil, StateCarryPreviousUnreadable
	}
	after, err := reconciler.levelDetectFingerprints(ctx, current.plan.Plan, current.dataset)
	if err != nil {
		return nil, StateCarryPreviousUnreadable
	}
	levels := make([]uint32, 0, len(after))
	for level, fingerprint := range after {
		if old, present := before[level]; present && old == fingerprint {
			levels = append(levels, level)
		}
	}
	if len(levels) == 0 {
		return nil, StateCarryDetectChanged
	}
	if len(levels) < len(after) {
		// Some Level's detection changed. The warming guard covers the whole
		// Plan, so a carry that spared only some Levels would buy nothing
		// and cost a second guard shape; the Plan warms up whole as before.
		return nil, StateCarryPartial
	}
	sort.Slice(levels, func(left, right int) bool { return levels[left] < levels[right] })
	return &execution.StateCarry{From: previous.Fact.Selected.StateGeneration, Levels: levels}, StateCarryCarried
}

// previousPlan is the Plan the previous activation ran, from the object its
// Query Group was published with. The Query Group is looked for first under
// the identity the Plan's group has now -- a Query Group is named by its
// query, which an edit to a trigger, a recovery or a formula does not touch --
// and then among the previous groups that list their Plans. A manifest lists
// a group's content and not its Plans, so a Plan whose group was renamed by
// the edit is not found there, and carries nothing.
func (reconciler *ScheduleActivationReconciler) previousPlan(
	ctx context.Context,
	previous PlanActivationRecord,
	previousContent activatedContent,
	currentGroup execution.QueryGroupIdentity,
) (contract.EvaluationPlanV2, contract.DatasetContractV2, map[execution.LogicalQueryRef]execution.QueryPlanFacts, error) {
	key := previous.Fact.Key()
	type found struct {
		plan    contract.EvaluationPlanV2
		dataset contract.DatasetContractV2
		queries map[execution.LogicalQueryRef]execution.QueryPlanFacts
	}
	find := func(identity execution.QueryGroupIdentity) (found, bool, error) {
		digest := previousContent.digests[identity]
		if digest == "" {
			return found{}, false, nil
		}
		object, err := reconciler.repository.LoadQueryGroupObject(ctx, digest)
		if err != nil {
			return found{}, false, err
		}
		for _, plan := range object.Plans {
			if plan.Key() != key {
				continue
			}
			strategyIR := plan.StrategyIR
			strategyIR.StrategyRef = plan.Strategy
			return found{plan: contract.EvaluationPlanV2{
				PlanID: plan.PlanID, StrategyRef: plan.Strategy, InputProjection: plan.InputProjection,
				OutputIdentity: plan.OutputIdentity, TargetScope: plan.TargetScope, TargetPlan: plan.TargetPlan,
				NoData: plan.NoData, EffectiveTimeSnapshot: plan.EffectiveTimeSnapshot,
				StrategyIR: strategyIR, TerminalReasonCode: plan.TerminalReasonCode,
			}, dataset: object.QueryPlan.Normalization.DatasetContract, queries: plan.QueryPlans}, true, nil
		}
		return found{}, false, nil
	}
	// The group the Plan is in now, which is where nearly every Plan was
	// before: one object read, no scan.
	if previous, ok, err := find(currentGroup); err != nil || ok {
		return previous.plan, previous.dataset, previous.queries, err
	}
	// Otherwise the groups that list their Plans. A manifest's do not, so
	// this is the open-Segment source only, and a Plan whose group was
	// renamed under a manifest carries nothing.
	for identity, group := range previousContent.groups {
		if identity == currentGroup {
			continue
		}
		for _, plan := range group.Plans {
			if plan.Key() != key {
				continue
			}
			if previous, ok, err := find(identity); err != nil || ok {
				return previous.plan, previous.dataset, previous.queries, err
			}
			break
		}
	}
	return contract.EvaluationPlanV2{}, contract.DatasetContractV2{}, nil, errors.New("alarmd controlplane: previous activation does not hold the Plan")
}

func (reconciler *ScheduleActivationReconciler) levelDetectFingerprints(
	ctx context.Context,
	plan contract.EvaluationPlanV2,
	dataset contract.DatasetContractV2,
) (map[uint32]string, error) {
	result, err := reconciler.compiler.Compile(ctx, strategy.CompileRequest{Plan: plan, DatasetContract: dataset, StateSemantics: reconciler.stateSemantics})
	if err != nil {
		return nil, err
	}
	compiled, ok := result.Plan()
	if !ok || result.PlanTerminal() != nil {
		return nil, errors.New("alarmd controlplane: Plan does not compile")
	}
	fingerprints := make(map[uint32]string, compiled.Levels().Len())
	for _, level := range compiled.Levels().All() {
		fingerprints[level.Definition().LevelID] = level.Fingerprints().Detect
	}
	return fingerprints, nil
}

// No-data carry outcomes, one per activation whose state generation moved
// while its Plan stayed active and whose new version keeps no-data state:
// no-data memory, a no-data end index, or both.
const (
	NoDataCarryCarried            = "carried"
	NoDataCarryEndsOnly           = "carried_ends_only"
	NoDataCarrySemanticsChanged   = "not_carried_semantics_changed"
	NoDataCarryPreviousUnreadable = "not_carried_previous_unreadable"
)

// NoDataCarryOutcomes are the outcomes in the order a reader groups them.
var NoDataCarryOutcomes = []string{NoDataCarryCarried, NoDataCarryEndsOnly, NoDataCarrySemanticsChanged,
	NoDataCarryPreviousUnreadable}

// observeNoDataCarry reports one activation's no-data carry outcome.
func (reconciler *ScheduleActivationReconciler) observeNoDataCarry(ctx context.Context, outcome string) {
	reconciler.repository.observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageStateCarryDecided,
		Result:     observability.ResultSuccess,
		StateCarry: &observability.StateCarryFacts{Scope: "no_data_plan", Result: outcome, Count: 1},
	})
}

// noDataCarry decides what of its no-data state an activation whose state
// generation moved while its Plan stayed active reads from the generation it
// moved from. It is decided from the two versions of the Plan alone, never
// from what the state holds.
//
// Both parts need the same query: the same metrics, conditions and grouping
// (the Plan's query facts), projected onto the same dimensions, under the
// same alert identity. A group key or a series digest is then the same name
// for the same data under either generation, and an end entry's fingerprint
// names the same alert. The memory needs more: no-data on in both versions
// with the same compiled section - dimensions, continuous count, level,
// group bound, horizon and recovery switch - so an absence it holds means
// what the new version would have recorded. The end index needs a Level
// that ends on no data in the new version; which entries follow is the
// Worker's to filter by Level, against the version it runs.
//
// Anything else is not carried, and the edit that changed it is the one that
// decides what its open alerts become. Returns no outcome when the new
// version keeps no no-data state at all.
func (reconciler *ScheduleActivationReconciler) noDataCarry(
	ctx context.Context,
	previous PlanActivationRecord,
	previousContent activatedContent,
	current changedPlan,
) (*execution.NoDataCarry, string) {
	after, err := reconciler.compiledPlan(ctx, current.plan.Plan, current.dataset)
	if err != nil {
		return nil, NoDataCarryPreviousUnreadable
	}
	if after.NoData() == nil && (after.OutputIdentity() == nil || !endsOnNoData(after)) {
		return nil, ""
	}
	previousPlan, previousDataset, previousQueries, err := reconciler.previousPlan(ctx, previous, previousContent, current.group)
	if err != nil {
		return nil, NoDataCarryPreviousUnreadable
	}
	before, err := reconciler.compiledPlan(ctx, previousPlan, previousDataset)
	if err != nil {
		return nil, NoDataCarryPreviousUnreadable
	}
	return decideNoDataCarry(previous.Fact.Selected.StateGeneration,
		noDataCarrySide{queries: previousQueries, projection: previousPlan.InputProjection, identity: before.OutputIdentity(),
			noData: before.NoData(), endsOnNoData: endsOnNoData(before)},
		noDataCarrySide{queries: current.plan.QueryPlans, projection: current.plan.Plan.InputProjection, identity: after.OutputIdentity(),
			noData: after.NoData(), endsOnNoData: endsOnNoData(after)})
}

// noDataCarrySide is what the no-data carry reads off one version of a Plan.
type noDataCarrySide struct {
	queries      map[execution.LogicalQueryRef]execution.QueryPlanFacts
	projection   contract.InputProjectionV2
	identity     *contract.MonitorOutputIdentity
	noData       *contract.NoDataConfigV1
	endsOnNoData bool
}

// decideNoDataCarry is noDataCarry's decision over the two versions, once
// both were read.
func decideNoDataCarry(from execution.StateGeneration, before, after noDataCarrySide) (*execution.NoDataCarry, string) {
	memoryKept := after.noData != nil
	endsKept := after.identity != nil && after.endsOnNoData
	if !memoryKept && !endsKept {
		return nil, ""
	}
	sameQuery := sameCanonical(before.queries, after.queries) &&
		sameCanonical(before.projection, after.projection) &&
		sameCanonical(before.identity, after.identity)
	carry := &execution.NoDataCarry{From: from}
	carry.Memory = memoryKept && sameQuery && before.noData != nil && sameCanonical(before.noData, after.noData)
	carry.Ends = endsKept && sameQuery
	switch {
	case !carry.Memory && !carry.Ends:
		return nil, NoDataCarrySemanticsChanged
	case memoryKept && !carry.Memory:
		return carry, NoDataCarryEndsOnly
	}
	return carry, NoDataCarryCarried
}

// endsOnNoData is whether any Level of a compiled Plan ends its alert on no
// data, which is what keeps a no-data end index for it.
func endsOnNoData(plan *strategy.CompiledPlan) bool {
	for _, level := range plan.Levels().All() {
		if level.Recovery().MissingData == contract.RecoveryMissingDataEvaluateAsZero {
			return true
		}
	}
	return false
}

// sameCanonical compares two values by their encoding, which orders map keys
// and so names one value one way.
func sameCanonical(left, right any) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}

func (reconciler *ScheduleActivationReconciler) compiledPlan(
	ctx context.Context,
	plan contract.EvaluationPlanV2,
	dataset contract.DatasetContractV2,
) (*strategy.CompiledPlan, error) {
	result, err := reconciler.compiler.Compile(ctx, strategy.CompileRequest{Plan: plan, DatasetContract: dataset, StateSemantics: reconciler.stateSemantics})
	if err != nil {
		return nil, err
	}
	compiled, ok := result.Plan()
	if !ok || result.PlanTerminal() != nil {
		return nil, errors.New("alarmd controlplane: Plan does not compile")
	}
	return compiled, nil
}
