// Copyright (C) 2022 THL A29 Limited, a Tencent company.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

// Encode owned versions once; cache immutable blocks by their row index sequence.
import (
	"bytes"
	"context"
	"encoding/binary"
	stdjson "encoding/json"
	"fmt"
	"math/bits"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/cespare/xxhash/v2"
)

type (
	snapshotPlanRow struct {
		encoded  []byte
		boundary uint64
	}
	snapshotPlanBlock   struct{ key, encoded []byte }
	snapshotPlanBuilder struct {
		ctx          context.Context
		rows         []snapshotPlanRow
		pending      []int
		key          []byte
		pendingBytes int
		size         int
		parts        [][]byte
		cache        map[uint64][]snapshotPlanBlock
		hits         int
	}
)

func (b *snapshotPlanBuilder) addRow(data []byte, boundary uint64) int {
	id := int(len(b.rows))
	b.rows = append(b.rows, snapshotPlanRow{data, boundary})
	return id
}

func (b *snapshotPlanBuilder) append(id int) error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	row := b.rows[id]
	if len(row.encoded) > int(^uint(0)>>1)-b.size {
		return fmt.Errorf("size overflow")
	}
	b.size += len(row.encoded)
	b.pendingBytes += len(row.encoded)
	b.pending = append(b.pending, id)
	b.key = binary.LittleEndian.AppendUint64(b.key, uint64(id))
	if b.pendingBytes >= topologyChunkTargetBytes || (b.pendingBytes >= topologyChunkMinBytes && row.boundary&127 == 0) {
		return b.flush()
	}
	return nil
}

func (b *snapshotPlanBuilder) flush() error {
	if len(b.pending) == 0 {
		return b.ctx.Err()
	}
	hash := xxhash.Sum64(b.key)
	for _, block := range b.cache[hash] {
		if bytes.Equal(block.key, b.key) {
			b.parts = append(b.parts, block.encoded)
			b.hits += len(block.encoded)
			b.pending = b.pending[:0]
			b.key = b.key[:0]
			b.pendingBytes = 0
			return nil
		}
	}
	data := make([]byte, 0, b.pendingBytes)
	for _, id := range b.pending {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		data = append(data, b.rows[id].encoded...)
	}
	b.cache[hash] = append(b.cache[hash], snapshotPlanBlock{append([]byte(nil), b.key...), data})
	b.parts = append(b.parts, data)
	b.pending = b.pending[:0]
	b.key = b.key[:0]
	b.pendingBytes = 0
	return nil
}

func (b *snapshotPlanBuilder) appendRows(ids []int) error {
	for i, id := range ids {
		if i&255 == 0 {
			if err := b.ctx.Err(); err != nil {
				return err
			}
		}
		row := b.rows[id]
		if len(row.encoded) > int(^uint(0)>>1)-b.size {
			return fmt.Errorf("size overflow")
		}
		b.size += len(row.encoded)
		b.pendingBytes += len(row.encoded)
		b.pending = append(b.pending, id)
		b.key = binary.LittleEndian.AppendUint64(b.key, uint64(id))
		if b.pendingBytes >= topologyChunkTargetBytes || (b.pendingBytes >= topologyChunkMinBytes && row.boundary&127 == 0) {
			if err := b.flush(); err != nil {
				return err
			}
		}
	}
	return b.ctx.Err()
}

func snapshotPlanMask(ctx context.Context, mask cmdb.SharedTopologyTimeMask, frames int, fn func(int)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for word := 0; word <= len(mask.Rest); word++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		value := mask.First
		if word > 0 {
			value = mask.Rest[word-1]
		}
		for value != 0 {
			bit := bits.TrailingZeros64(value)
			i := word*64 + bit
			if i >= frames {
				break
			}
			fn(i)
			value &= value - 1
		}
	}
	return nil
}

func marshalTopologySnapshotPlan(ctx context.Context, item cmdb.SharedTopologyResponseData, plan *cmdb.SharedTopologySnapshotPlan) ([][]byte, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if plan == nil {
		return nil, 0, fmt.Errorf("snapshot plan is nil")
	}
	b := snapshotPlanBuilder{ctx: ctx, cache: make(map[uint64][]snapshotPlanBlock)}
	frames := len(plan.Timestamps)
	nodes := make([][]int, frames)
	edges := make([][]int, frames)
	nodeCounts, edgeCounts := make([]int, frames), make([]int, frames)
	for _, node := range plan.Nodes {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		if e := snapshotPlanMask(ctx, node.Mask, frames, func(i int) { nodeCounts[i]++ }); e != nil {
			return nil, 0, e
		}
	}
	for _, edge := range plan.Edges {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		if e := snapshotPlanMask(ctx, edge.Mask, frames, func(i int) { edgeCounts[i]++ }); e != nil {
			return nil, 0, e
		}
	}
	for i := range nodes {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		nodes[i] = make([]int, 0, nodeCounts[i])
		edges[i] = make([]int, 0, edgeCounts[i])
	}
	b.rows = make([]snapshotPlanRow, 0, 2*(len(plan.Nodes)+len(plan.Edges))+100)
	b.pending = make([]int, 0, 512)
	b.key = make([]byte, 0, 2048)
	addVersion := func(encoded []byte, mask cmdb.SharedTopologyTimeMask, lists [][]int) error {
		encoded = append(encoded, ',')
		boundary := xxhash.Sum64(encoded)
		id := b.addRow(encoded, boundary)
		b.addRow(encoded[:len(encoded)-1], boundary)
		return snapshotPlanMask(ctx, mask, frames, func(i int) { lists[i] = append(lists[i], id) })
	}
	for _, node := range plan.Nodes {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		data, e := marshalTopologyItem(node.Node)
		if e != nil {
			return nil, 0, e
		}
		if e = addVersion(data, node.Mask, nodes); e != nil {
			return nil, 0, e
		}
	}
	for _, edge := range plan.Edges {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		data, e := marshalTopologyItem(edge.Edge)
		if e != nil {
			return nil, 0, e
		}
		if e = addVersion(data, edge.Mask, edges); e != nil {
			return nil, 0, e
		}
	}
	rawIDs := make(map[string]int)
	raw := func(s string) error {
		id, ok := rawIDs[s]
		if !ok {
			id = b.addRow([]byte(s), 1)
			rawIDs[s] = id
		}
		return b.append(id)
	}
	header, e := stdjson.Marshal(struct {
		Code  int    `json:"code"`
		Start int64  `json:"start_time"`
		End   int64  `json:"end_time"`
		Step  string `json:"step"`
		Count int    `json:"point_count"`
	}{item.Code, item.StartTime, item.EndTime, item.Step, item.PointCount})
	if e != nil {
		return nil, 0, e
	}
	if e = raw(string(header[:len(header)-1]) + `,"snapshots":[`); e != nil {
		return nil, 0, e
	}
	partial := make(map[int]string)
	for _, p := range plan.Partial {
		partial[p.Index] = p.Reason
	}
	// Plans remain owned and non-destructive for callers.
	appendSection := func(list []int) error {
		if len(list) > 0 {
			list[len(list)-1]++
		}
		return b.appendRows(list)
	}
	for i, ts := range plan.Timestamps {
		if e = raw(`{"timestamp":` + strconv.FormatInt(ts, 10) + `,"nodes":[`); e != nil {
			return nil, 0, e
		}
		if e = b.flush(); e != nil {
			return nil, 0, e
		}
		if e = appendSection(nodes[i]); e != nil {
			return nil, 0, e
		}
		if e = raw("]"); e != nil {
			return nil, 0, e
		}
		if e = b.flush(); e != nil {
			return nil, 0, e
		}
		if e = raw(`,"edges":[`); e != nil {
			return nil, 0, e
		}
		if e = appendSection(edges[i]); e != nil {
			return nil, 0, e
		}
		if e = raw("]"); e != nil {
			return nil, 0, e
		}
		reason, isPartial := partial[i]
		trailer, e := stdjson.Marshal(struct {
			Partial bool   `json:"partial"`
			Reason  string `json:"partial_reason,omitempty"`
		}{isPartial, reason})
		if e != nil {
			return nil, 0, e
		}
		if e = raw("," + string(trailer[1:])); e != nil {
			return nil, 0, e
		}
		if i+1 < frames {
			if e = raw(","); e != nil {
				return nil, 0, e
			}
		}
		if e = b.flush(); e != nil {
			return nil, 0, e
		}
	}
	if e = raw("]"); e != nil {
		return nil, 0, e
	}
	if item.Message != "" {
		data, e := stdjson.Marshal(item.Message)
		if e != nil {
			return nil, 0, e
		}
		if e = raw(`,"message":` + string(data)); e != nil {
			return nil, 0, e
		}
	}
	if e = raw("}"); e != nil {
		return nil, 0, e
	}
	if e = b.flush(); e != nil {
		return nil, 0, e
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	return b.parts, b.size, nil
}
