// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package observability

import "context"

type traceFieldsContextKey struct{}

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
