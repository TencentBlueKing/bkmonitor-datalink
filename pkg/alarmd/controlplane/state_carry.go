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
	StateCarryCarried = "carried"
	// StateCarryCarriedDetectChanged is a carry in which some Level's detection
	// changed: its results are carried as they were decided.
	StateCarryCarriedDetectChanged = "carried_detect_changed"
	// StateCarryGroupChanged is an edit that moved the Plan to another Query
	// Group while its state generation moved. A Query Group is named by its
	// query facts - the query, its delay, the detection step, the shard,
	// named outputs, normalization and global business scope - so the edit
	// changed the data the Plan reads or how it is read: the old results are
	// about other data, and nothing is carried (a deliberate
	// difference from Python, which keys its results by item and keeps them).
	StateCarryGroupChanged = "none_group_changed"
	// StateCarryNoCommonLevel is a previous version read back whole that
	// shares no Level with the new one, every Level id changed: nothing to
	// carry.
	StateCarryNoCommonLevel      = "none_no_common_level"
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
// the old state and now, and it warms up whole. Every other carried record
// carries and is not forced to warm up: its first Slot under the new
// generation writes the carried history and evaluates on it, as the Slots
// after it would. A warm-up would re-decide nothing - the state holds each
// point's decided result, which stands as decided (stateCarry) - and only
// cost the Slot it took and the absent point it left in every window. A
// series the previous generation held nothing for is short of points and
// stays WARMING on its own window, as a new series does.
func applyStateCarries(records []PlanActivationRecord, carries map[int]*execution.StateCarry, returned map[int]struct{}) (carried, discontinuous int) {
	for index, carry := range carries {
		if _, back := returned[index]; back {
			discontinuous++
			continue
		}
		records[index].Fact.Selected.Carry = carry
		records[index].Fact.Selected.RequiredFullSlots = 1
		records[index].Fact.Selected.ForceWarming = false
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
var StateCarryOutcomes = []string{StateCarryCarried, StateCarryCarriedDetectChanged, StateCarryGroupChanged, StateCarryNoCommonLevel,
	StateCarryPreviousUnreadable, StateCarryDiscontinuous}

// stateCarry decides what an activation whose state generation moved while
// its Plan stayed active may carry over: every Level present in both versions
// of the Plan. Their stored results are the anomaly or normal each point was
// decided as, and stand as decided - the trigger and the recovery are
// summarized from those results every time, so a changed trigger or
// recovery reads the old results the way it would have read them all along,
// and a changed threshold or algorithm counts the points its old detection
// decided beside the ones the new one decides, as Python does: its check
// results are keyed by strategy, item, dimensions and level, and no edit
// clears them. The Worker writes a carried result under the
// Level's detect fingerprint now (carryHistory).
//
// One edit carries nothing: one that moved the Plan to another Query Group.
// A Query Group is named by its query facts, so that edit changed the data
// the Plan reads or how, and the old results are about other data
// (StateCarryGroupChanged): the previous version is not under the group the
// Plan is in now.
//
// The previous version is read back by the content digest the previous
// activation ran and compiled again. When it cannot be, nothing is carried
// and the Plan warms up whole, as it did before any of this.
func (reconciler *ScheduleActivationReconciler) stateCarry(
	ctx context.Context,
	previous PlanActivationRecord,
	previousContent activatedContent,
	current changedPlan,
) (*execution.StateCarry, string) {
	previousPlan, previousDataset, _, err := reconciler.previousPlan(ctx, previous, previousContent, current.group)
	if errors.Is(err, errPlanLeftItsGroup) {
		return nil, StateCarryGroupChanged
	}
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
	detectChanged := false
	for level, fingerprint := range after {
		old, present := before[level]
		if !present {
			continue
		}
		levels = append(levels, level)
		detectChanged = detectChanged || old != fingerprint
	}
	if len(levels) == 0 {
		return nil, StateCarryNoCommonLevel
	}
	sort.Slice(levels, func(left, right int) bool { return levels[left] < levels[right] })
	carry := &execution.StateCarry{From: previous.Fact.Selected.StateGeneration, Levels: levels}
	if detectChanged {
		return carry, StateCarryCarriedDetectChanged
	}
	return carry, StateCarryCarried
}

// previousPlan is the Plan the previous activation ran, from the object its
// Query Group was published with, under the identity the Plan's group has
// now: a Query Group is named by its query facts, which an edit to a
// trigger, a recovery, a threshold or a formula does not touch. Neither
// source of the previous content lists a group's Plans (a manifest names a
// group and its object, an open Segment its object), so a Plan the edit
// moved to another group is not looked for elsewhere: errPlanLeftItsGroup.
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
			if _, listed := previousContent.groups[identity]; listed {
				// The group was there and its object is not named: the
				// previous version cannot be read, the group did not change.
				return found{}, false, errPreviousGroupWithoutObject
			}
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
	previousPlan, ok, err := find(currentGroup)
	if err != nil || ok {
		return previousPlan.plan, previousPlan.dataset, previousPlan.queries, err
	}
	return contract.EvaluationPlanV2{}, contract.DatasetContractV2{}, nil, errPlanLeftItsGroup
}

// errPlanLeftItsGroup is a Plan the previous activation does not hold under
// the Query Group it is in now: the edit moved it to another group, which is
// an edit to its query facts.
//
// errPreviousGroupWithoutObject is the Query Group the Plan is in now present
// in the previous content with no object named for it (a blocked group that
// had not activated, applyBlockedToActivatedContent): the previous version
// cannot be read, which is not a group change.
var errPreviousGroupWithoutObject = errors.New("alarmd controlplane: previous content names the Query Group without its object")

var errPlanLeftItsGroup = errors.New("alarmd controlplane: previous activation does not hold the Plan under its Query Group")

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
	if errors.Is(err, errPlanLeftItsGroup) {
		// Another Query Group is another query: the no-data state is about
		// other data, as decideNoDataCarry says of a changed query.
		return nil, NoDataCarrySemanticsChanged
	}
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
