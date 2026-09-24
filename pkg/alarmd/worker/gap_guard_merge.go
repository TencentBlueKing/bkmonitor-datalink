// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"errors"
	"fmt"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// GapGuardDisagreement is two series batches of one Slot saying different
// things about one Plan's gap marker.
//
// A typed error rather than a sentence because it reaches the completion line,
// and a refusal with no word arrives there as an error nobody can group, count
// or tell apart from the next unnamed one - which is how a Query Group
// conflicting every other Slot for half an hour once read as an unclassified
// defect.
type GapGuardDisagreement struct {
	// Detail says what disagreed, for a human reading the error. The reason
	// code is what the line carries.
	Detail string
}

func (disagreement *GapGuardDisagreement) Error() string {
	return "alarmd worker: " + disagreement.Detail
}

// GapGuardDisagreeReason returns the bounded reason when err is the merge
// refusing two batches that disagree, so an observer names it rather than
// calling it unknown.
func GapGuardDisagreeReason(err error) (execution.ReasonCode, bool) {
	var disagreement *GapGuardDisagreement
	if !errors.As(err, &disagreement) || disagreement == nil {
		return "", false
	}
	return execution.ReasonCode(contract.ReasonGapGuardDisagree), true
}

func gapGuardDisagree(format string, args ...any) error {
	return &GapGuardDisagreement{Detail: fmt.Sprintf(format, args...)}
}

// mergeGapGuardStatements folds one series batch's gap marker statements into
// the ones the Slot has accumulated.
//
// A Slot writes one statement to a Plan's marker, and until now the two guard
// lists were appended to independently: a batch that opened and a batch that
// cleared each kept its own, and the Slot ended carrying both. The result
// contract forbids that shape, but it is checked on each batch's own result,
// so no batch ever saw it - the Slot did, and failed on it, every other round.
// Production only ever showed this shape and never two statements in one list,
// which is what the digest-equal append already collapsed.
//
// The three rules below are one idea: the Slot's statement must be what a Slot
// that had evaluated every series in one batch would have said.
func mergeGapGuardStatements(plan *execution.PlanEvaluationResult, next execution.PlanEvaluationResult) error {
	opened, err := unionGapOpenStatements(plan.GuardBeforeEvents, next.GuardBeforeEvents)
	if err != nil {
		return err
	}
	cleared, err := agreedGapClearStatement(plan.GuardAfterState, next.GuardAfterState)
	if err != nil {
		return err
	}
	// An open supersedes a clear, which is the rule a single batch already
	// applies to itself when it finds an incomplete input: it sets the open and
	// drops the clear in the same breath. Applying it only within a batch is
	// what let one batch's clear outlive another batch's open. The direction is
	// the safe one either way - clearing a marker while part of the Slot did
	// not complete would report the gap closed over data nobody read.
	if len(opened) != 0 {
		cleared = nil
	}
	plan.GuardBeforeEvents, plan.GuardAfterState = opened, cleared
	return nil
}

// unionGapOpenStatements merges the opens into the single statement a
// one-batch Slot would have produced.
//
// Everything but the scopes is identical across batches by construction: the
// marker they read is the Slot's loaded one, the warmup target is a function
// of the compiled Plan, and the apply version comes from the frozen contract.
// So the scopes are unioned, and a scope both batches name keeps the folded
// reason rather than whichever batch was merged last.
//
// The fold is the same function the single-batch path uses, deliberately.
// The result contract compares a degraded Level's reason against the reason on
// a marker of its scope, and those two hold together only because both are the
// same fold over the same inputs. Choosing here instead - first wins, last
// wins, strongest kind - would be a second derivation for the first to
// disagree with, on exactly the rounds where two batches failed differently.
func unionGapOpenStatements(current, next []execution.PlanGapMutation) ([]execution.PlanGapMutation, error) {
	combined := make([]execution.PlanGapMutation, 0, len(current)+len(next))
	combined = append(combined, current...)
	combined = append(combined, next...)
	if len(combined) <= 1 {
		return combined, nil
	}
	base := combined[0]
	// Batches proposing the same statement have nothing to merge, and this is
	// the ordinary case: every batch of a Slot whose incomplete inputs are the
	// same reaches the same scopes and the same fold. Returning it untouched
	// also keeps the statement the producer built rather than one rebuilt from
	// its parts, so nothing downstream sees a digest that moved for no reason.
	if identicalGapStatements(combined) {
		return []execution.PlanGapMutation{base}, nil
	}
	scopes := make(map[execution.GapScope]execution.GapScopeMutation, len(base.Scopes))
	for _, statement := range combined {
		if err := gapStatementsAgreeOutsideScopes(base, statement); err != nil {
			return nil, err
		}
		for _, scope := range statement.Scopes {
			existing, seen := scopes[scope.Scope]
			if !seen {
				scopes[scope.Scope] = scope
				continue
			}
			merged, err := foldGapScopeMutation(existing, scope)
			if err != nil {
				return nil, err
			}
			scopes[scope.Scope] = merged
		}
	}
	ordered := make([]execution.GapScopeMutation, 0, len(scopes))
	for _, scope := range scopes {
		ordered = append(ordered, scope)
	}
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].Scope.HasLevel != ordered[right].Scope.HasLevel {
			return !ordered[left].Scope.HasLevel
		}
		return ordered[left].Scope.LevelID < ordered[right].Scope.LevelID
	})
	// Rebuilt rather than edited: the digest closes the scopes, and a merged
	// statement carrying the digest of one of its halves is a statement the
	// store would idempotently mistake for that half.
	merged, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{
		Identity: base.Identity, ExpectedMarkerRevision: base.ExpectedMarkerRevision,
		ApplyVersion: base.ApplyVersion, ScheduleRevision: base.ScheduleRevision,
		Scopes: ordered,
	})
	if err != nil {
		return nil, fmt.Errorf("alarmd worker: merge Slot gap opens: %w", err)
	}
	return []execution.PlanGapMutation{merged}, nil
}

// identicalGapStatements reports whether every statement says the same thing,
// by the three fields that make two statements the same write: what they
// address, what they contain, and what they expect to find there.
func identicalGapStatements(statements []execution.PlanGapMutation) bool {
	base := statements[0]
	for _, statement := range statements[1:] {
		if statement.Identity != base.Identity ||
			statement.MutationDigest != base.MutationDigest ||
			statement.ExpectedMarkerRevision != base.ExpectedMarkerRevision {
			return false
		}
	}
	return true
}

// foldGapScopeMutation merges two batches' statements about one scope.
func foldGapScopeMutation(existing, candidate execution.GapScopeMutation) (execution.GapScopeMutation, error) {
	if existing.Kind != candidate.Kind {
		return execution.GapScopeMutation{}, gapGuardDisagree(
			"two series batches propose %s and %s for one gap scope",
			existing.Kind, candidate.Kind)
	}
	if existing.RequiredFullSlots != candidate.RequiredFullSlots {
		// Derived from the compiled Plan, which is one object for the Slot, so
		// a disagreement means the batches did not evaluate the same Plan.
		return execution.GapScopeMutation{}, gapGuardDisagree(
			"two series batches require %d and %d FULL warmup slots for one gap scope",
			existing.RequiredFullSlots, candidate.RequiredFullSlots)
	}
	existing.ReasonCode = execution.ReasonCode(contract.FoldGapReason(
		[]string{string(existing.ReasonCode), string(candidate.ReasonCode)}))
	return existing, nil
}

// agreedGapClearStatement keeps the clear every batch agreed on, and refuses
// two that disagree.
//
// A clear is derived from the marker the Slot loaded, not from the batch, so
// every batch that proposes one proposes the same one; the digest-equal append
// already collapsed those. Two that differ therefore mean two batches read
// different markers for one Plan in one Slot, which is not a shape to pick a
// winner from - whichever were chosen, the other batch's series were evaluated
// against a marker the Slot then denies.
//
// The expected revision is compared as well as the digest, because the digest
// does not close it: two clears agreeing on content while disagreeing on the
// revision they expect would collapse into one, and the survivor would carry
// an expectation the other half never had.
func agreedGapClearStatement(current, next []execution.PlanGapMutation) ([]execution.PlanGapMutation, error) {
	combined := make([]execution.PlanGapMutation, 0, len(current)+len(next))
	combined = append(combined, current...)
	combined = append(combined, next...)
	if len(combined) <= 1 {
		return combined, nil
	}
	base := combined[0]
	for _, statement := range combined[1:] {
		if statement.Identity != base.Identity {
			return nil, gapGuardDisagree("two series batches clear different Plan gap markers")
		}
		if statement.MutationDigest != base.MutationDigest {
			return nil, gapGuardDisagree(
				"two series batches clear one Plan gap marker differently (%s and %s)",
				base.MutationDigest, statement.MutationDigest)
		}
		if statement.ExpectedMarkerRevision != base.ExpectedMarkerRevision {
			return nil, gapGuardDisagree(
				"two series batches clear one Plan gap marker expecting revision %d and %d",
				base.ExpectedMarkerRevision, statement.ExpectedMarkerRevision)
		}
	}
	return []execution.PlanGapMutation{base}, nil
}

// gapStatementsAgreeOutsideScopes checks the fields the merge assumes are the
// Slot's rather than the batch's.
func gapStatementsAgreeOutsideScopes(base, candidate execution.PlanGapMutation) error {
	switch {
	case candidate.Identity != base.Identity:
		return gapGuardDisagree("two series batches open different Plan gap markers")
	case candidate.ExpectedMarkerRevision != base.ExpectedMarkerRevision:
		return gapGuardDisagree("two series batches open one Plan gap marker expecting revision %d and %d",
			base.ExpectedMarkerRevision, candidate.ExpectedMarkerRevision)
	case candidate.ApplyVersion != base.ApplyVersion:
		return gapGuardDisagree("two series batches open one Plan gap marker under different apply versions")
	case candidate.ScheduleRevision != base.ScheduleRevision:
		return gapGuardDisagree("two series batches open one Plan gap marker under different schedule revisions")
	}
	return nil
}
