// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"math"
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type topologyTraversalReservationKey struct{}

// traversalReservationBytes covers request-owned workspaces that coexist with
// the retained shared graph: root masks, the filtered edge slice, reachable and
// three propagation maps (old frontier, discovered, next frontier). The map
// allowance is the same explicit capacity estimate as graph accounting, not an
// exact Go heap/RSS bound. Relation strings and state masks are borrowed. The
// normal contract has at most 60 frames, so its timeBitmap needs no separately
// allocated trailing words; larger internal grids retain a word allowance.
func traversalReservationBytes(nodes, roots, edges, points int) (int64, error) {
	if nodes < 0 || roots < 0 || edges < 0 || points < 0 {
		return 0, fmt.Errorf("negative topology traversal size")
	}
	size := int64(5)*topologyMapHeader + 64*8
	add := func(count int, width int64) error {
		if count > 0 && width > (math.MaxInt64-size)/int64(count) {
			return fmt.Errorf("topology traversal reservation overflow")
		}
		size += int64(count) * width
		return nil
	}
	wordBytes := int64(max(points-1, 0)/64) * 8
	mapEntry := graphMapEntry(8, unsafe.Sizeof(timeBitmap{})) + 2*wordBytes
	for _, part := range []struct {
		count int
		width int64
	}{
		{roots, mapEntry},
		{nodes, 4 * mapEntry},
		{edges, int64(unsafe.Sizeof(sharedTopologyEdgeState{}))},
		{points, 3 * 8},    // two frame-count arrays plus timestamp-to-info indices
		{3, 2 * wordBytes}, // per-edge/per-node intermediate bitmap values
	} {
		if err := add(part.count, part.width); err != nil {
			return 0, err
		}
	}
	return size, nil
}

// Call with exclusive request ownership or the graph read lock. The model owns
// this stage from before root-mask copying through final target filtering;
// direct graph APIs acquire it themselves. Nested exports reuse the same stage.
func (q *TimeGraph) withTraversalReservation(ctx context.Context) (context.Context, func(), error) {
	noop := func() {}
	if q.shared == nil || yoloMode {
		return ctx, noop, nil
	}
	if owner, _ := ctx.Value(topologyTraversalReservationKey{}).(*TimeGraph); owner == q {
		return ctx, noop, nil
	}
	_, span := trace.NewSpan(ctx, "timegraph-reserve-traversal")
	var err error
	defer func() { span.End(&err) }()
	want, err := traversalReservationBytes(len(q.shared.nodeBits), len(q.shared.relationNodeBits), q.shared.edgeCount(), len(q.shared.grid.timestamps))
	if err != nil {
		return ctx, noop, err
	}
	span.Set("traversal-reserved-bytes", want)
	span.Set("traversal-nodes", len(q.shared.nodeBits))
	span.Set("traversal-edges", q.shared.edgeCount())
	if err = ReserveTopologyStage(ctx, "traversal", want); err != nil {
		return ctx, noop, err
	}
	// This transient conservative workspace does not claim materialized credit.
	// It remains pending until scope exit, avoiding ownership assertions for maps
	// replaced between hops and reused/borrowed strings.
	release := func() { _ = ReserveTopologyStage(ctx, "traversal", 0) }
	return context.WithValue(ctx, topologyTraversalReservationKey{}, q), release, nil
}
