// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

type sampleLimitRead struct {
	metric     string
	conditions structured.Conditions
	points     int
	samples    int
}

// sampleLimitBackend 在 hopFetchBackend 之上按响应的样本数拒收：样本数超过 maxSamples 的
// 响应按单次响应过大处理，时间减半后样本数随之减少。maxSamples 为负时不拒收；fail 非空时
// 该指标的读取返回普通错误。
type sampleLimitBackend struct {
	inner              *hopFetchBackend
	maxSamples         int
	fail               string
	accepted, rejected []sampleLimitRead
	failed             int
}

func (b *sampleLimitBackend) query(ctx context.Context, ts *structured.QueryTs, expr string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, bool, error) {
	name := ts.QueryList[0].FieldName
	if frontierProbeForTest(ts) {
		return b.inner.query(ctx, ts, expr, instant, start, end, step)
	}
	if name == b.fail {
		b.failed++
		return nil, false, errors.New("backend unavailable")
	}
	matrix, partial, err := b.inner.query(ctx, ts, expr, instant, start, end, step)
	if err != nil {
		return nil, false, err
	}
	read := sampleLimitRead{metric: name, conditions: cloneContractConditions(ts.QueryList[0].Conditions), points: len(legacyRangeGridPoints(start, end, step))}
	for _, series := range matrix {
		read.samples += len(series.Points)
	}
	if b.maxSamples >= 0 && read.samples > b.maxSamples {
		b.rejected = append(b.rejected, read)
		return nil, false, metadata.ErrBackendResponseTooLarge
	}
	b.accepted = append(b.accepted, read)
	return matrix, partial, nil
}

// queryTopologyWithBackend 用同一后端分别取快照和紧凑格式，返回按身份规范化的两种答案。
func queryTopologyWithBackend(t *testing.T, backend *sampleLimitBackend, request cmdb.SharedTopologyQuery) ([]string, []string, error) {
	t.Helper()
	model := &Model{schemaProvider: hopFetchProvider(), timeGraphQueryReference: selectorQueryReference, timeGraphVMQueryWithPartial: backend.query}
	request.ResponseFormat = ""
	result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
	if err != nil {
		return nil, nil, err
	}
	request.ResponseFormat = cmdb.CompactTopologyFormat
	compact, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
	require.NoError(t, err)
	return canonicalTopologyForTest(t, result.Snapshots), canonicalTopologyForTest(t, decodeCompactForTest(t, compact.Compact)), nil
}

func timeSplitFixture() map[string]pl.Matrix {
	frames := hopFetchFrames()
	fixture := newHopFetchFixture()
	for p := 0; p < 4; p++ {
		pod := fmt.Sprintf("p%d", p)
		fixture.add("node_with_pod", map[string]string{"cluster": "c0", "node": "node0", "pod": pod}, frames...)
		containers := 1
		if p == 0 {
			containers = 6
		}
		for c := 0; c < containers; c++ {
			labels := map[string]string{"cluster": "c0", "pod": pod, "name": fmt.Sprintf("n%d", c)}
			fixture.add("container_with_pod", labels, frames...)
			fixture.add("container_info_relation", labels, frames...)
		}
	}
	return fixture.data
}

func hasEqualCondition(conditions structured.Conditions, field, value string) bool {
	for _, condition := range conditions.FieldList {
		if condition.DimensionName == field && condition.Operator == structured.ConditionEqual && len(condition.Value) == 1 && condition.Value[0] == value {
			return true
		}
	}
	return false
}

// 单次后端响应超过边界时，共享拓扑把根读取、单个身份的前沿读取、整类关系的宽读取和
// info 读取按时间对半重读，直到单个评估点：快照与紧凑格式都与不拆分的答案相同，
// 每次被接受的读取都在边界内。
func TestTopologyTimeSplitMatchesUnsplitAnswer(t *testing.T) {
	oldPlan, oldReuse := SharedTopologyPlanCandidates, SharedTopologyReuseMatrix
	t.Cleanup(func() { SharedTopologyPlanCandidates, SharedTopologyReuseMatrix = oldPlan, oldReuse })
	SharedTopologyPlanCandidates, SharedTopologyReuseMatrix = true, true
	frames := hopFetchFrames()
	data := timeSplitFixture()
	const limit = 10
	for _, tc := range []struct {
		name    string
		request cmdb.SharedTopologyQuery
		split   func(read sampleLimitRead) bool
	}{
		{
			name:    "wide_read",
			request: cmdb.SharedTopologyQuery{SourceType: "node"},
			split: func(read sampleLimitRead) bool {
				return read.metric == "node_with_pod" && !hopFetchNarrowed(read.conditions)
			},
		},
		{
			name:    "root_read",
			request: cmdb.SharedTopologyQuery{SourceType: "pod", SourceInfo: cmdb.Matcher{"cluster": "c0", "pod": "p0"}},
			split: func(read sampleLimitRead) bool {
				return read.metric == "container_with_pod" && hasEqualCondition(read.conditions, "pod", "p0") && len(read.conditions.ConditionList) == len(read.conditions.FieldList)-1
			},
		},
		{
			name:    "single_identity_read",
			request: cmdb.SharedTopologyQuery{SourceType: "pod", SourceInfo: cmdb.Matcher{"cluster": "c0", "pod": "p0"}},
			split: func(read sampleLimitRead) bool {
				return read.metric == "node_with_pod" && hasEqualCondition(read.conditions, "node", "node0")
			},
		},
		{
			name:    "info_read",
			request: cmdb.SharedTopologyQuery{SourceType: "container", SourceInfo: cmdb.Matcher{"cluster": "c0"}, SourceExpandInfo: cmdb.Matcher{"pod": "p0"}},
			split:   func(read sampleLimitRead) bool { return read.metric == "container_info_relation" },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := tc.request
			request.SpaceUID, request.MaxHops, request.Step = "space", 2, "60s"
			request.StartTime, request.EndTime = frames[0]/1000, frames[len(frames)-1]/1000
			wantSnapshots, wantCompact, err := queryTopologyWithBackend(t, &sampleLimitBackend{inner: &hopFetchBackend{t: t, data: data}, maxSamples: -1}, request)
			require.NoError(t, err)

			backend := &sampleLimitBackend{inner: &hopFetchBackend{t: t, data: data}, maxSamples: limit}
			gotSnapshots, gotCompact, err := queryTopologyWithBackend(t, backend, request)
			require.NoError(t, err)
			require.Equal(t, wantSnapshots, gotSnapshots)
			require.Equal(t, wantCompact, gotCompact)
			split := false
			for _, read := range backend.rejected {
				split = split || tc.split(read)
			}
			require.True(t, split, "the fixture must reject this kind of read: %+v", backend.rejected)
			for _, read := range backend.accepted {
				require.LessOrEqual(t, read.samples, limit)
			}
		})
	}
}

// 单个评估点仍超过边界时返回终态错误，原因 max_response_bytes；普通错误不触发拆分。
func TestTopologyTimeSplitTerminalAndOtherErrors(t *testing.T) {
	frames := hopFetchFrames()
	request := cmdb.SharedTopologyQuery{
		SpaceUID: "space", SourceType: "pod", SourceInfo: cmdb.Matcher{"cluster": "c0", "pod": "p0"}, MaxHops: 2, Step: "60s",
		StartTime: frames[0] / 1000, EndTime: frames[len(frames)-1] / 1000,
	}
	t.Run("one_point_still_too_large", func(t *testing.T) {
		// p0 的 6 个容器在单个评估点就有 6 个样本。
		backend := &sampleLimitBackend{inner: &hopFetchBackend{t: t, data: timeSplitFixture()}, maxSamples: 5}
		_, _, err := queryTopologyWithBackend(t, backend, request)
		var terminal *UnsplittableResponseError
		require.ErrorAs(t, err, &terminal)
		require.ErrorIs(t, err, metadata.ErrBackendResponseTooLarge)
		require.Equal(t, "container_with_pod", terminal.Metric)
		require.ErrorContains(t, err, "topology resource limit exceeded: max_response_bytes")
		onePoint := false
		for _, read := range backend.rejected {
			onePoint = onePoint || (read.metric == "container_with_pod" && read.points == 1)
		}
		require.True(t, onePoint, "the read is split down to one evaluation point before failing")
	})
	t.Run("other_errors_do_not_split", func(t *testing.T) {
		backend := &sampleLimitBackend{inner: &hopFetchBackend{t: t, data: timeSplitFixture()}, maxSamples: -1, fail: "node_with_pod"}
		_, _, err := queryTopologyWithBackend(t, backend, request)
		require.ErrorContains(t, err, "backend unavailable")
		var terminal *UnsplittableResponseError
		require.False(t, errors.As(err, &terminal))
		require.Equal(t, 1, backend.failed, "a failed read is not retried in halves")
	})
}
