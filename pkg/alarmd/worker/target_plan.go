// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// resolvedTarget is one Plan's target plan as this Slot resolved it: the
// member set the admission filter asks about, the reduced view absence
// reads, and the summary the round commits. All come out of one
// resolution, which is the point of holding them together.
type resolvedTarget struct {
	absence nodata.TargetResolution
	members map[string]struct{}
	facts   execution.TargetResolutionSummary
	// unresolved is true when nothing resolved the plan at all - no resolver
	// on this worker - as opposed to a resolution one of whose selectors
	// could not answer. Only the first admits nothing; the second admits the
	// members that did resolve.
	unresolved bool
	// definitive is the resolution's own account of whether its members are
	// the whole target; see Definitive.
	definitive bool
}

// summary is what the round's completion carries for this Plan.
func (target *resolvedTarget) summary(strategyID string) execution.TargetResolutionSummary {
	summary := target.facts
	summary.StrategyID = strategyID
	return summary
}

// Contains is the admission filter's question.
func (target *resolvedTarget) Contains(key string) bool {
	if target == nil {
		return false
	}
	_, found := target.members[key]
	return found
}

// Definitive says whether "not a member" is the target's own answer: the
// plan was resolved, every selector answered in full from current facts,
// and no reference pointed at a node the topology cache does not list or
// lists under another business. Anything less is a lower bound on the
// target, which may filter a record for this Slot but must not close the
// record's alert. See admission.DefinitelyOutside.
func (target *resolvedTarget) Definitive() bool {
	if target == nil || target.unresolved {
		return false
	}
	return target.definitive
}

// absenceView is what the no-data round reads; nil when nothing resolved.
func (target *resolvedTarget) absenceView() *nodata.TargetResolution {
	if target == nil {
		return nil
	}
	view := target.absence
	return &view
}

// ResolvedTargets hands the source what Begin resolved, so the records it
// admits are filtered against the same members absence is judged against.
//
// Every resolution is handed over, whatever its state: a selector that
// could not answer contributes no members, and the members the static list
// and the other selectors did resolve still admit their records - "no
// match" is per selector, and a group key gone missing must not stop the
// static hosts beside it from being detected. Only a Plan nothing resolved
// at all is left out; its filter then admits nothing, under the name that
// says so.
func (stream *streamedExecution) ResolvedTargets() execution.TargetMemberships {
	if len(stream.targetResolutions) == 0 {
		return nil
	}
	targets := make(execution.TargetMemberships, len(stream.targetResolutions))
	for identity, resolution := range stream.targetResolutions {
		if resolution == nil || resolution.unresolved {
			continue
		}
		targets[identity] = resolution
	}
	if len(targets) == 0 {
		return nil
	}
	return targets
}

// resolveTargetPlans resolves every due Plan's target plan once for this
// Slot, at Begin, so the records that arrive afterwards and the absence
// judged at the end read one value. A Plan without a target plan is not
// touched; a worker without a resolver resolves nothing, and every such
// Plan is then unresolved by name on both sides.
func (stream *streamedExecution) resolveTargetPlans(ctx context.Context) {
	resolver := stream.coordinator.ports.Targets
	for _, due := range stream.header.DuePlans {
		plan := due.CompiledPlan.TargetPlan()
		if plan == nil {
			continue
		}
		if stream.targetResolutions == nil {
			stream.targetResolutions = make(map[execution.PlanIdentity]*resolvedTarget)
		}
		var resolution *targetplan.Resolution
		if resolver != nil {
			interval := time.Duration(due.CompiledPlan.EvaluationSemantics().EvaluationInterval) * time.Second
			resolution = resolver.Resolve(ctx, plan, interval)
		}
		unresolved := resolution == nil
		if unresolved {
			// Nothing resolved it: unavailable by name, and the source is
			// what is missing.
			resolution = &targetplan.Resolution{Selectors: []targetplan.SelectorResult{{
				Kind: targetplan.SelectorKindStatic, State: targetplan.SelectorUnavailable, Reason: targetplan.ReasonSourceUnwired}}}
			resolution.Compose()
		}
		target := newResolvedTarget(resolution)
		target.unresolved = unresolved
		stream.targetResolutions[due.Identity] = target
		stream.observeTargetResolution(ctx, due, resolution)
	}
}

// newResolvedTarget projects one resolution into the two views the Slot
// reads: the member set for admission (nil when unavailable, which the
// filter reads as "admit nothing") and the reduced view for absence.
func newResolvedTarget(resolution *targetplan.Resolution) *resolvedTarget {
	target := &resolvedTarget{absence: nodata.TargetResolution{}}
	switch resolution.State {
	case targetplan.ResolutionComplete:
		target.absence.State = nodata.TargetResolutionComplete
	case targetplan.ResolutionIncomplete:
		target.absence.State = nodata.TargetResolutionIncomplete
	default:
		target.absence.State = nodata.TargetResolutionUnavailable
	}
	target.absence.Members = resolution.Members()
	target.definitive = resolution.State == targetplan.ResolutionComplete && resolution.StaleAge == 0 &&
		len(resolution.Failures) == 0 && len(resolution.NodesMissing) == 0 && len(resolution.NodesForeign) == 0
	// One union, read twice: the absence view lists it and the filter
	// indexes it, so the two cannot disagree about who the members are.
	members := resolution.Members()
	target.members = make(map[string]struct{}, len(members))
	for _, key := range members {
		target.members[key] = struct{}{}
	}
	target.facts = execution.TargetResolutionSummary{
		State: string(resolution.State), NodesMissing: append([]string(nil), resolution.NodesMissing...),
		NodesForeign: append([]string(nil), resolution.NodesForeign...), StaleAgeSeconds: int64(resolution.StaleAge.Seconds()),
	}
	for _, failure := range resolution.Failures {
		target.facts.Failures = append(target.facts.Failures, execution.TargetSelectorFailure{
			Kind: failure.Kind, ID: failure.ID, Reason: failure.Reason, Dropped: failure.Dropped, Kept: failure.Kept,
		})
	}
	return target
}

// observeTargetResolution writes the one line and the two counters a
// resolution leaves: the composed state, the stale age when a selector ate
// old grain, and each selector by name.
func (stream *streamedExecution) observeTargetResolution(ctx context.Context, due execution.DuePlan, resolution *targetplan.Resolution) {
	facts := &observability.TargetResolutionFacts{
		StrategyID: due.Identity.StrategyID, State: string(resolution.State),
		StaleAgeSeconds: int64(resolution.StaleAge.Seconds()),
		Selectors:       make([]observability.TargetSelectorFacts, 0, len(resolution.Selectors)),
	}
	for _, selector := range resolution.Selectors {
		facts.Selectors = append(facts.Selectors, observability.TargetSelectorFacts{
			Kind: selector.Kind, ID: selector.ID, State: string(selector.State), Reason: selector.Reason,
			Kept: selector.Kept, Dropped: selector.Dropped, NodeMissing: selector.NodeMissing, NodeForeign: selector.NodeForeign,
		})
	}
	result := observability.Result(observability.ResultSuccess)
	if resolution.State != targetplan.ResolutionComplete {
		result = observability.ResultDegraded
	}
	stream.coordinator.emitObservation(ctx, observability.Observation{
		Component: observability.ComponentAccess, Stage: observability.StageTargetResolved,
		Operation: observability.Operation(stream.request.Operation), Direction: observability.DirectionInternal,
		Result: result, TargetResolution: facts,
	})
}

// targetSummaries reduces this Slot's resolutions to what the round's
// completion carries, in Plan order so two commits of one round encode the
// same bytes.
func (stream *streamedExecution) targetSummaries() []execution.TargetResolutionSummary {
	if len(stream.targetResolutions) == 0 {
		return nil
	}
	summaries := make([]execution.TargetResolutionSummary, 0, len(stream.targetResolutions))
	for _, due := range stream.header.DuePlans {
		target, resolved := stream.targetResolutions[due.Identity]
		if !resolved || target == nil {
			continue
		}
		summaries = append(summaries, target.summary(due.Identity.StrategyID))
	}
	return summaries
}
