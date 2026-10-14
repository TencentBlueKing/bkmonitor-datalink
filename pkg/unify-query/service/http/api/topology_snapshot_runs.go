// Copyright (C) 2022 THL A29 Limited, a Tencent company.
// Licensed under the MIT License.
package api

import (
	"context"
	stdjson "encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

type snapshotMaskRun struct {
	mask  cmdb.SharedTopologyTimeMask
	parts [][]byte
}

func sameSnapshotMask(a, b cmdb.SharedTopologyTimeMask) bool {
	return a.First == b.First && slices.Equal(a.Rest, b.Rest)
}

func marshalTopologySnapshotPlan(ctx context.Context, item cmdb.SharedTopologyResponseData, plan *cmdb.SharedTopologySnapshotPlan) ([][]byte, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if plan == nil {
		return nil, 0, fmt.Errorf("snapshot plan is nil")
	}
	runs := 0
	for i, n := range plan.Nodes {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		if i == 0 || !sameSnapshotMask(n.Mask, plan.Nodes[i-1].Mask) {
			runs++
		}
	}
	for i, e := range plan.Edges {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		if i == 0 || !sameSnapshotMask(e.Mask, plan.Edges[i-1].Mask) {
			runs++
		}
	}
	// This selects an encoding strategy; high churn keeps the indexed builder.
	// Neither path changes rows, frames, JSON fields, or response limits.
	if len(plan.Timestamps) > 1 && runs > 0 && (len(plan.Nodes)+len(plan.Edges))/runs >= 16 {
		return marshalTopologySnapshotRuns(ctx, item, plan)
	}
	return marshalTopologySnapshotPlanIndexed(ctx, item, plan)
}

// Equal-mask contiguous rows have identical frame membership. Encode the run
// once, then reference immutable byte blocks in every participating frame.
// All row JSON and frame headers are encoded before any HTTP write.
func marshalTopologySnapshotRuns(ctx context.Context, item cmdb.SharedTopologyResponseData, plan *cmdb.SharedTopologySnapshotPlan) ([][]byte, int, error) {
	nodes, err := buildSnapshotMaskRuns(ctx, len(plan.Nodes), func(i int) cmdb.SharedTopologyTimeMask { return plan.Nodes[i].Mask }, func(i int) ([]byte, error) { return marshalTopologyItem(plan.Nodes[i].Node) })
	if err != nil {
		return nil, 0, err
	}
	type edgeLabels struct {
		RelationType string `json:"relation_type"`
		MetricName   string `json:"metric_name"`
		Category     string `json:"category"`
		Direction    string `json:"direction"`
	}
	suffixes := make(map[edgeLabels][]byte)
	edges, err := buildSnapshotMaskRuns(ctx, len(plan.Edges), func(i int) cmdb.SharedTopologyTimeMask { return plan.Edges[i].Mask }, func(i int) ([]byte, error) {
		edge := plan.Edges[i].Edge
		labels := edgeLabels{edge.RelationType, edge.MetricName, edge.Category, edge.Direction}
		suffix, ok := suffixes[labels]
		if !ok {
			encoded, err := stdjson.Marshal(labels)
			if err != nil {
				return nil, err
			}
			suffix = append([]byte{','}, encoded[1:]...)
			suffixes[labels] = suffix
		}
		encoded := make([]byte, 0, 64+len(suffix))
		encoded = append(encoded, `{"source":`...)
		encoded = strconv.AppendUint(encoded, edge.Source, 10)
		encoded = append(encoded, `,"target":`...)
		encoded = strconv.AppendUint(encoded, edge.Target, 10)
		return append(encoded, suffix...), nil
	})
	if err != nil {
		return nil, 0, err
	}
	frames := len(plan.Timestamps)
	frameRuns := func(runs []snapshotMaskRun) ([][]int, error) {
		result := make([][]int, frames)
		for i, run := range runs {
			if err := snapshotPlanMask(ctx, run.mask, frames, func(frame int) { result[frame] = append(result[frame], i) }); err != nil {
				return nil, err
			}
		}
		return result, nil
	}
	nodeFrames, err := frameRuns(nodes)
	if err != nil {
		return nil, 0, err
	}
	edgeFrames, err := frameRuns(edges)
	if err != nil {
		return nil, 0, err
	}
	parts := [][]byte{}
	size := 0
	add := func(data []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(data) > int(^uint(0)>>1)-size {
			return fmt.Errorf("size overflow")
		}
		size += len(data)
		parts = append(parts, data)
		return nil
	}
	raw := func(s string) error { return add([]byte(s)) }
	header, err := stdjson.Marshal(struct {
		Code  int    `json:"code"`
		Start int64  `json:"start_time"`
		End   int64  `json:"end_time"`
		Step  string `json:"step"`
		Count int    `json:"point_count"`
	}{item.Code, item.StartTime, item.EndTime, item.Step, item.PointCount})
	if err != nil {
		return nil, 0, err
	}
	if err = raw(string(header[:len(header)-1]) + `,"snapshots":[`); err != nil {
		return nil, 0, err
	}
	appendSection := func(runs []snapshotMaskRun, indices []int) error {
		for i, id := range indices {
			blocks := runs[id].parts
			for j, data := range blocks {
				if i == len(indices)-1 && j == len(blocks)-1 {
					data = data[:len(data)-1]
				}
				if err := add(data); err != nil {
					return err
				}
			}
		}
		return nil
	}
	partial := map[int]string{}
	for _, p := range plan.Partial {
		partial[p.Index] = p.Reason
	}
	for i, ts := range plan.Timestamps {
		if err = raw(`{"timestamp":` + strconv.FormatInt(ts, 10) + `,"nodes":[`); err != nil {
			return nil, 0, err
		}
		if err = appendSection(nodes, nodeFrames[i]); err != nil {
			return nil, 0, err
		}
		if err = raw(`],"edges":[`); err != nil {
			return nil, 0, err
		}
		if err = appendSection(edges, edgeFrames[i]); err != nil {
			return nil, 0, err
		}
		reason, isPartial := partial[i]
		trailer, err := stdjson.Marshal(struct {
			Partial bool   `json:"partial"`
			Reason  string `json:"partial_reason,omitempty"`
		}{isPartial, reason})
		if err != nil {
			return nil, 0, err
		}
		if err = raw("]," + string(trailer[1:])); err != nil {
			return nil, 0, err
		}
		if i+1 < frames {
			if err = raw(","); err != nil {
				return nil, 0, err
			}
		}
	}
	if err = raw("]"); err != nil {
		return nil, 0, err
	}
	if item.Message != "" {
		data, err := stdjson.Marshal(item.Message)
		if err != nil {
			return nil, 0, err
		}
		if err = raw(`,"message":` + string(data)); err != nil {
			return nil, 0, err
		}
	}
	if err = raw("}"); err != nil {
		return nil, 0, err
	}
	if err = ctx.Err(); err != nil {
		return nil, 0, err
	}
	return parts, size, nil
}

func buildSnapshotMaskRuns(ctx context.Context, count int, maskAt func(int) cmdb.SharedTopologyTimeMask, rowAt func(int) ([]byte, error)) ([]snapshotMaskRun, error) {
	runs := []snapshotMaskRun{}
	for first := 0; first < count; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		mask := maskAt(first)
		last := first + 1
		for last < count && sameSnapshotMask(mask, maskAt(last)) {
			if last&255 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			last++
		}
		run := snapshotMaskRun{mask: mask}
		var work []byte
		for i := first; i < last; i++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			data, err := rowAt(i)
			if err != nil {
				return nil, err
			}
			if work == nil {
				capacity := len(data) + 1
				if capacity < topologyChunkTargetBytes {
					if last-i < topologyChunkTargetBytes/capacity {
						capacity *= last - i
					} else {
						capacity = topologyChunkTargetBytes
					}
				}
				work = make([]byte, 0, capacity)
			}
			work = append(work, data...)
			work = append(work, ',')
			if len(work) >= topologyChunkTargetBytes {
				run.parts = append(run.parts, work)
				work = nil
			}
		}
		if len(work) > 0 {
			run.parts = append(run.parts, work)
		}
		runs = append(runs, run)
		first = last
	}
	return runs, nil
}
