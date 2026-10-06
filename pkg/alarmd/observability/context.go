// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package observability

import "context"

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
