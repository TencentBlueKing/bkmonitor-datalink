// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package observability

import (
	"context"
	"sync"
	"time"
)

type traceFieldsContextKey struct{}

type heldByContextKey struct{}

// ContextWithHeldBy carries what the round before this one did with the
// Query Group. The scheduler's Runner sets it before the Slot source and the
// executor are called, so both the decision to give a Slot up and the
// completion that reports it can name the holder on their own line.
//
// A context value rather than a parameter, and here rather than in the
// scheduler, because the places that report it are reached through
// interfaces the Runner does not own and live in three packages: the Slot
// source and the runtime's executor wrapper, which named it already, and the
// Coordinator's Progress commit, which did not -- and the fleet reads the
// round's completion from the commit, so its record of a skipped Slot never
// said what held it. Threading a diagnostic through those signatures would
// put it in contracts that have nothing else to do with it.
func ContextWithHeldBy(ctx context.Context, facts HeldByFacts) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, heldByContextKey{}, facts)
}

// HeldByFromContext is what the previous round did with this Query Group.
//
// Never nil: a round that held nothing reports the word for that, and so does
// the first round of all, which has no round before it. Returning nil for
// those was the bug -- the claim was that "none" is a word and not a missing
// key, and production showed the key simply absent on every line that should
// have carried it, which is the state a reader cannot tell from "this build
// does not report held_by at all". A distribution needs its commonest value
// present to be a distribution.
func HeldByFromContext(ctx context.Context) *HeldByFacts {
	if ctx == nil {
		return &HeldByFacts{Decision: HeldByNothing}
	}
	facts, ok := ctx.Value(heldByContextKey{}).(HeldByFacts)
	if !ok || facts.Decision == "" {
		return &HeldByFacts{Decision: HeldByNothing}
	}
	return &facts
}

type divertDecisionContextKey struct{}

// ContextWithDivertDecision carries the decision that let a Slot run as a
// normal round: the scheduler's Runner sets it before the executor is
// called, and the executor's completion line names it when the round ends
// timed out. Through the context for the reason HeldBy is.
func ContextWithDivertDecision(ctx context.Context, facts DivertDecisionFacts) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, divertDecisionContextKey{}, facts)
}

// DivertDecisionFromContext is the decision that let this Slot run as a
// normal round, or nil when the Slot was not judged (a replay, a probe, or
// a source without recovery limits).
func DivertDecisionFromContext(ctx context.Context) *DivertDecisionFacts {
	if ctx == nil {
		return nil
	}
	facts, ok := ctx.Value(divertDecisionContextKey{}).(DivertDecisionFacts)
	if !ok {
		return nil
	}
	return &facts
}

// DivertDecisionWithBudget is the decision on ctx as a lost round's line
// gives it: a round not judged by the divert (bound source none) has its
// remaining time measured against the deadline its read ran under, from the
// round's budget; any other decision is as it was judged.
func DivertDecisionWithBudget(ctx context.Context, budget *BudgetCapture) *DivertDecisionFacts {
	decision := DivertDecisionFromContext(ctx)
	if decision != nil && decision.BoundSource == DivertBoundNone {
		if facts := budget.Facts(); facts != nil && facts.QueryDeadlineUnixMilli > 0 {
			decision.RemainingMillis = facts.QueryDeadlineUnixMilli - decision.ClassifiedAtUnixMilli
		}
	}
	return decision
}

type budgetCaptureContextKey struct{}

// BudgetCapture keeps the query budget of the round's binding consumer, as
// access resolved it, for the round's completion line. The executor puts one
// on the context of each attempt; access records into it; the completion
// line of a round that ends degraded renders it, so a lost round carries its
// own budget without a line on every round that succeeds.
type BudgetCapture struct {
	mu        sync.Mutex
	facts     *QueryBudgetFacts
	startedAt int64
	// failedRequest and failedPlanned are RecordFailedQuery's.
	failedRequest *QueryRequestFacts
	failedPlanned *QueryPlannedFacts
}

// Record keeps facts when they bind the round tighter than what is kept: the
// earliest query deadline the read is cut at.
func (capture *BudgetCapture) Record(facts QueryBudgetFacts) {
	if capture == nil {
		return
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if capture.facts == nil || facts.QueryDeadlineUnixMilli < capture.facts.QueryDeadlineUnixMilli {
		facts.QueryStartedAtUnixMilli = max(facts.QueryStartedAtUnixMilli, capture.startedAt)
		capture.facts = &facts
	}
}

// RecordFailedQuery keeps the round's first failed physical query's request
// facts and plan.
func (capture *BudgetCapture) RecordFailedQuery(request *QueryRequestFacts, planned *QueryPlannedFacts) {
	if capture == nil || (request == nil && planned == nil) {
		return
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if capture.failedRequest != nil || capture.failedPlanned != nil {
		return
	}
	capture.failedRequest, capture.failedPlanned = request, planned
}

// MarkStarted keeps when a query of the round was sent, the latest of them.
func (capture *BudgetCapture) MarkStarted(at time.Time) {
	if capture == nil {
		return
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if at.UnixMilli() > capture.startedAt {
		capture.startedAt = at.UnixMilli()
	}
	if capture.facts != nil {
		capture.facts.QueryStartedAtUnixMilli = capture.startedAt
	}
}

// Facts is the binding consumer's budget recorded so far, or nil.
func (capture *BudgetCapture) Facts() *QueryBudgetFacts {
	if capture == nil {
		return nil
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if capture.facts == nil {
		return nil
	}
	facts := *capture.facts
	facts.FailedRequest, facts.FailedPlanned = capture.failedRequest, capture.failedPlanned
	return &facts
}

// ContextWithBudgetCapture carries a fresh BudgetCapture for one attempt.
func ContextWithBudgetCapture(ctx context.Context) (context.Context, *BudgetCapture) {
	if ctx == nil {
		ctx = context.Background()
	}
	capture := &BudgetCapture{}
	return context.WithValue(ctx, budgetCaptureContextKey{}, capture), capture
}

// BudgetCaptureFromContext is the attempt's BudgetCapture, or nil.
func BudgetCaptureFromContext(ctx context.Context) *BudgetCapture {
	if ctx == nil {
		return nil
	}
	capture, _ := ctx.Value(budgetCaptureContextKey{}).(*BudgetCapture)
	return capture
}

func ContextWithTraceFields(ctx context.Context, fields TraceFields) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, traceFieldsContextKey{}, mergeTraceFields(fields, TraceFieldsFromContext(ctx)))
}

func TraceFieldsFromContext(ctx context.Context) TraceFields {
	if ctx == nil {
		return TraceFields{}
	}
	fields, _ := ctx.Value(traceFieldsContextKey{}).(TraceFields)
	return fields
}

func mergeTraceFields(primary, fallback TraceFields) TraceFields {
	if primary.TraceID == "" {
		primary.TraceID = fallback.TraceID
	}
	if primary.ExecutionID == "" {
		primary.ExecutionID = fallback.ExecutionID
	}
	if primary.MessageID == "" {
		primary.MessageID = fallback.MessageID
	}
	if primary.QueryGroupKey == "" {
		primary.QueryGroupKey = fallback.QueryGroupKey
	}
	if primary.SnapshotRevision == "" {
		primary.SnapshotRevision = fallback.SnapshotRevision
	}
	if primary.QueryRevision == "" {
		primary.QueryRevision = fallback.QueryRevision
	}
	if primary.ScheduleRevision == "" {
		primary.ScheduleRevision = fallback.ScheduleRevision
	}
	if primary.ScheduleSegmentStart == 0 {
		primary.ScheduleSegmentStart = fallback.ScheduleSegmentStart
	}
	if primary.DuePlanSetDigest == "" {
		primary.DuePlanSetDigest = fallback.DuePlanSetDigest
	}
	if primary.OwnerID == "" {
		primary.OwnerID = fallback.OwnerID
	}
	if primary.OwnerEpoch == 0 {
		primary.OwnerEpoch = fallback.OwnerEpoch
	}
	if primary.EvaluationTime == 0 {
		primary.EvaluationTime = fallback.EvaluationTime
	}
	if primary.StrategyID == "" {
		primary.StrategyID = fallback.StrategyID
	}
	if primary.BusinessID == "" {
		primary.BusinessID = fallback.BusinessID
	}
	if primary.LevelID == "" {
		primary.LevelID = fallback.LevelID
	}
	if primary.TerminalScope == "" {
		primary.TerminalScope = fallback.TerminalScope
	}
	if primary.TerminalFieldPath == "" {
		primary.TerminalFieldPath = fallback.TerminalFieldPath
	}
	if primary.RecordID == "" {
		primary.RecordID = fallback.RecordID
	}
	if primary.DimensionIdentityDigest == "" {
		primary.DimensionIdentityDigest = fallback.DimensionIdentityDigest
	}
	if primary.Topic == "" {
		primary.Topic = fallback.Topic
	}
	if !primary.PartitionKnown && fallback.PartitionKnown {
		primary.Partition, primary.PartitionKnown = fallback.Partition, true
	}
	if !primary.OffsetKnown && fallback.OffsetKnown {
		primary.Offset, primary.OffsetKnown = fallback.Offset, true
	}
	if primary.SourceWindow == "" {
		primary.SourceWindow = fallback.SourceWindow
	}
	return primary
}
