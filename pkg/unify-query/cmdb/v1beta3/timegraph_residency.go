// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

// Graph and Matrix accounting uses calibratable reservation models,
// not RSS guarantees. String retention is conservatively counted per owner;
// maps use an explicit capacity allowance, never runtime heap deltas.
import (
	"context"
	"fmt"
	"math"
	"sync"
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	pl "github.com/prometheus/prometheus/promql"
)

const (
	graphReservationChunk int64 = 256 << 10
	topologyMapHeader     int64 = 64
	topologyMapEntryExtra int64 = 16
)

// Two slots per live entry cover load-factor/capacity slack and bounded growth.
// The allowance is explicit so measured Go/platform evidence can calibrate it.
func graphMapEntry(key, value uintptr) int64 { return 2 * (int64(key+value) + topologyMapEntryExtra) }

func graphMatcherCost(info cmdb.Matcher) int64 {
	cost := topologyMapHeader
	for key, value := range info {
		cost += graphMapEntry(unsafe.Sizeof(key), unsafe.Sizeof(value)) + int64(len(key)+len(value))
	}
	return cost
}

func graphMergedMatcherCost(old, incoming cmdb.Matcher) int64 {
	cost := topologyMapHeader
	for key, value := range old {
		if v, ok := incoming[key]; ok && v > value {
			value = v
		}
		cost += graphMapEntry(unsafe.Sizeof(key), unsafe.Sizeof(value)) + int64(len(key)+len(value))
	}
	for key, value := range incoming {
		if _, ok := old[key]; !ok {
			cost += graphMapEntry(unsafe.Sizeof(key), unsafe.Sizeof(value)) + int64(len(key)+len(value))
		}
	}
	return cost
}

func graphNodeBuilderCost(indexes, fields cmdb.Index, info cmdb.Matcher, compressed bool) int64 {
	cost := 2*graphMapEntry(8, 8) + graphMapEntry(8, unsafe.Sizeof(cmdb.Matcher{})) + topologyMapHeader
	appendField := func(key, value string) {
		cost += graphMapEntry(unsafe.Sizeof(key), unsafe.Sizeof(value)) + int64(len(key)+len(value))
		if compressed {
			cost += graphMapEntry(8, 8) + 4*graphMapEntry(8, unsafe.Sizeof(value)) + int64(len(key)+len(value))
		}
	}
	for _, key := range indexes {
		appendField(key, info[key])
	}
	for _, key := range fields {
		if _, ok := info[key]; !ok {
			continue
		}
		duplicate := false
		for _, index := range indexes {
			if key == index {
				duplicate = true
				break
			}
		}
		if !duplicate {
			appendField(key, info[key])
		}
	}
	if compressed {
		cost += topologyMapHeader + graphMapEntry(8, 8)
	}
	return cost
}

// Compression stores uint64 pairs and may add two dictionary mappings for
// each new field/value. Existing dictionary entries stay live after a merge;
// counting the candidate strings conservatively avoids global dictionary scans.
func graphCompressedMapCost(fields int) int64 {
	return topologyMapHeader + int64(fields)*graphMapEntry(8, 8)
}

func graphCandidateDictionaryCost(info cmdb.Matcher) int64 {
	cost := int64(0)
	for key, value := range info {
		cost += 2*graphMapEntry(unsafe.Sizeof(key), 8) + 2*graphMapEntry(8, unsafe.Sizeof(value)) + int64(len(key)+len(value))
	}
	return cost
}

type graphResidencyStats struct {
	EstimatedBytes              int64 `json:"estimated_bytes"`
	ReservedBytes               int64 `json:"reserved_bytes"`
	MaterializedLowerBoundBytes int64 `json:"materialized_lower_bound_bytes"`
	PeakEstimatedBytes          int64 `json:"peak_estimated_bytes"`
	Adjustments                 int64 `json:"adjustments"`
	Reservations                int64 `json:"reservations"`
}
type graphResidency struct {
	mu    sync.Mutex
	ctx   context.Context
	stats graphResidencyStats
}

func (m *graphResidency) adjust(delta int64) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ctx.Err(); err != nil && delta > 0 {
		return err
	}
	if delta > 0 && m.stats.EstimatedBytes > math.MaxInt64-delta {
		return fmt.Errorf("graph residency estimate overflow")
	}
	next := m.stats.EstimatedBytes + delta
	if next < 0 {
		return fmt.Errorf("negative graph residency estimate")
	}
	want := m.stats.ReservedBytes
	if next > want {
		if next > math.MaxInt64-graphReservationChunk {
			return fmt.Errorf("graph reservation overflow")
		}
		want = (next + graphReservationChunk - 1) / graphReservationChunk * graphReservationChunk
		if err := ReserveTopologyStage(m.ctx, "graph", want); err != nil {
			return err
		}
		m.stats.Reservations++
		m.stats.ReservedBytes = want
	}
	m.stats.EstimatedBytes = next
	m.stats.Adjustments++
	m.stats.PeakEstimatedBytes = max(m.stats.PeakEstimatedBytes, next)
	return nil
}

func (m *graphResidency) commitOwned(delta int64) error {
	if m == nil || delta == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.stats.MaterializedLowerBoundBytes + delta
	if next < 0 || next > m.stats.ReservedBytes {
		return fmt.Errorf("invalid graph owned lower bound")
	}
	if err := CommitTopologyStage(m.ctx, "graph", next); err != nil {
		return err
	}
	m.stats.MaterializedLowerBoundBytes = next
	return nil
}

func (m *graphResidency) clear() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_ = ReserveTopologyStage(m.ctx, "graph", 0)
	m.stats.EstimatedBytes = 0
	m.stats.ReservedBytes = 0
	m.stats.MaterializedLowerBoundBytes = 0
}

func graphResidencyState(q *TimeGraph) graphResidencyStats {
	if q == nil || q.residency == nil {
		return graphResidencyStats{}
	}
	q.residency.mu.Lock()
	defer q.residency.mu.Unlock()
	return q.residency.stats
}

func (q *TimeGraph) enableResidency(ctx context.Context) error {
	if yoloMode || q.shared == nil || q.residency != nil {
		return nil
	}
	q.residency = &graphResidency{ctx: ctx}
	owned := int64(unsafe.Sizeof(*q) + unsafe.Sizeof(*q.nodeBuilder) + unsafe.Sizeof(*q.stringDict) + unsafe.Sizeof(*q.shared))
	if err := q.residency.adjust(owned + 32*topologyMapHeader); err != nil {
		return err
	}
	if err := q.residency.commitOwned(owned); err != nil {
		return err
	}
	q.nodeBuilder.residencyAdjust = q.residency.adjust
	q.matrixResidency = &matrixResidency{ctx: ctx, holders: map[string]matrixHolder{}}
	return nil
}

func graphVersionCost(versions []sharedNodeInfo) int64 {
	cost := int64(cap(versions)) * int64(unsafe.Sizeof(sharedNodeInfo{}))
	for _, v := range versions {
		cost += graphMatcherCost(v.dimensions) + int64(cap(v.activeBits.rest))*8
	}
	return cost
}

func graphSharedNodeIndexesCost() int64 {
	return graphMapEntry(8, unsafe.Sizeof([]sharedNodeInfo{})) + 3*graphMapEntry(8, unsafe.Sizeof(timeBitmap{}))
}

type graphInfoReservation struct {
	meter                   *graphResidency
	before, upper, oldOwned int64
	fresh                   bool
}

func (q *TimeGraph) reserveInfoChange(node uint64, info cmdb.Matcher, active timeBitmap) (graphInfoReservation, error) {
	if q.residency == nil {
		return graphInfoReservation{}, nil
	}
	old := q.shared.nodeInfos[node]
	r := graphInfoReservation{meter: q.residency, before: graphVersionCost(old), oldOwned: int64(len(old)) * int64(unsafe.Sizeof(sharedNodeInfo{})), fresh: len(old) == 0}
	if r.meter == nil {
		return r, nil
	}
	// Only this node's current versions are inspected; no whole-graph scan.
	// A split can produce two versions per old version plus one new remainder.
	capacity := 2 * (2*len(old) + 1)
	r.upper = int64(capacity) * int64(unsafe.Sizeof(sharedNodeInfo{}))
	remaining := active
	for _, v := range old {
		outside := v.activeBits.subtract(active)
		overlap := v.activeBits.intersect(active)
		if !outside.empty() {
			r.upper += graphMatcherCost(v.dimensions) + int64(cap(outside.rest))*8
		}
		if !overlap.empty() {
			r.upper += graphMergedMatcherCost(v.dimensions, info) + int64(cap(overlap.rest))*8
			remaining = remaining.subtract(overlap)
		}
	}
	if !remaining.empty() {
		r.upper += graphMatcherCost(info) + int64(cap(remaining.rest))*8
	}
	// The old versions remain live while replacement slices/maps are built.
	charge := r.upper
	if r.fresh {
		charge += graphSharedNodeIndexesCost()
	}
	return r, r.meter.adjust(charge)
}

func (r graphInfoReservation) settle(versions []sharedNodeInfo, success bool) error {
	if r.meter == nil {
		return nil
	}
	if !success {
		delta := -r.upper
		if r.fresh {
			delta -= graphSharedNodeIndexesCost()
		}
		return r.meter.adjust(delta)
	}
	if err := r.meter.adjust(graphVersionCost(versions) - r.before - r.upper); err != nil {
		return err
	}
	return r.meter.commitOwned(int64(len(versions))*int64(unsafe.Sizeof(sharedNodeInfo{})) - r.oldOwned)
}

func (q *TimeGraph) reserveSharedEdge(key timeGraphTopologyEdgeKey, pair timeGraphEdgeKey) error {
	if q.residency == nil {
		return nil
	}
	g := q.shared
	relID, knownRelation := g.relationIDs[key.relation]
	compact := sharedCompactEdgeKey{key.source, key.target, relID}
	exists := false
	switch {
	case g.wordEdges != nil:
		_, exists = g.wordEdges[compact]
	case g.compactEdges != nil:
		_, exists = g.compactEdges[compact]
	default:
		_, exists = g.edgeBits[key]
	}
	if exists {
		return nil
	}
	var cost int64
	switch {
	case g.wordEdges != nil:
		cost = graphMapEntry(unsafe.Sizeof(compact), 8)
	case g.compactEdges != nil:
		cost = graphMapEntry(unsafe.Sizeof(compact), unsafe.Sizeof(timeBitmap{}))
	default:
		cost = graphMapEntry(unsafe.Sizeof(key), unsafe.Sizeof(timeBitmap{}))
	}
	pairExists := false
	if g.wordPairs != nil {
		_, pairExists = g.wordPairs[pair]
	} else {
		_, pairExists = g.edgePairBits[pair]
	}
	if !pairExists {
		if g.wordPairs != nil {
			cost += graphMapEntry(unsafe.Sizeof(pair), 8)
		} else {
			cost += graphMapEntry(unsafe.Sizeof(pair), unsafe.Sizeof(timeBitmap{}))
		}
	}
	if (g.wordEdges != nil || g.compactEdges != nil) && !knownRelation {
		cost += graphMapEntry(unsafe.Sizeof(key.relation), 8) + 2*int64(unsafe.Sizeof(key.relation)) + int64(len(key.relation.relationType)+len(key.relation.metricName)+len(key.relation.category)+len(key.relation.direction))
	}
	return q.residency.adjust(cost)
}

type (
	matrixHolder         struct{ receiving, resident int64 }
	matrixResidencyStats struct {
		ReceivingBytes int64 `json:"receiving_bytes"`
		ResidentBytes  int64 `json:"resident_bytes"`
		PeakTotalBytes int64 `json:"peak_total_bytes"`
		Transitions    int64 `json:"transitions"`
		Holders        int   `json:"holders"`
	}
)

type matrixResidency struct {
	mu      sync.Mutex
	ctx     context.Context
	holders map[string]matrixHolder
	stats   matrixResidencyStats
}

// Bound physical topology fetches across requests in the process. A request
// waiting to apply a fetched Matrix does not occupy a backend slot.
var topologyBackendSlots = make(chan struct{}, 4)

func (m *matrixResidency) set(token string, next matrixHolder) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old := m.holders[token]
	receiving := m.stats.ReceivingBytes - old.receiving + next.receiving
	resident := m.stats.ResidentBytes - old.resident + next.resident
	if receiving < 0 || resident < 0 || receiving > math.MaxInt64-resident {
		return fmt.Errorf("invalid matrix residency estimate")
	}
	// No borrowed Matrix/string is claimed as owned materialization. Replacing a
	// holder cannot carry a previous object's materialized credit into a fetch.
	if err := CommitTopologyStage(m.ctx, "matrix", 0); err != nil {
		return err
	}
	if err := ReserveTopologyStage(m.ctx, "matrix", receiving+resident); err != nil {
		return err
	}
	if next == (matrixHolder{}) {
		delete(m.holders, token)
	} else {
		m.holders[token] = next
	}
	m.stats.ReceivingBytes = receiving
	m.stats.ResidentBytes = resident
	m.stats.PeakTotalBytes = max(m.stats.PeakTotalBytes, receiving+resident)
	m.stats.Transitions++
	m.stats.Holders = len(m.holders)
	return nil
}

func (m *matrixResidency) begin(token string, limit int64) error {
	if limit > math.MaxInt64/8 {
		return fmt.Errorf("matrix receive estimate overflow")
	}
	return m.set(token, matrixHolder{receiving: limit * 8})
}

func (m *matrixResidency) commit(token string, matrix pl.Matrix) error {
	return m.set(token, matrixHolder{resident: matrixResidentEstimate(matrix)})
}
func (m *matrixResidency) release(token string) error { return m.set(token, matrixHolder{}) }
func (m *matrixResidency) clear() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_ = ReserveTopologyStage(m.ctx, "matrix", 0)
	m.holders = map[string]matrixHolder{}
	m.stats.ReceivingBytes = 0
	m.stats.ResidentBytes = 0
	m.stats.Holders = 0
}

func matrixResidencyState(q *TimeGraph) matrixResidencyStats {
	if q == nil || q.matrixResidency == nil {
		return matrixResidencyStats{}
	}
	q.matrixResidency.mu.Lock()
	defer q.matrixResidency.mu.Unlock()
	return q.matrixResidency.stats
}

func matrixResidentEstimate(matrix pl.Matrix) int64 {
	total := int64(cap(matrix)) * int64(unsafe.Sizeof(pl.Series{}))
	for _, series := range matrix {
		total += int64(cap(series.Points))*int64(unsafe.Sizeof(pl.Point{})) + int64(cap(series.Metric))*int64(unsafe.Sizeof(series.Metric[0]))
		for _, label := range series.Metric {
			total += int64(len(label.Name) + len(label.Value))
		}
	}
	return total
}

func (loader *timeGraphMatrixLoader) matrixMemory() *matrixResidency {
	if !loader.topology || loader.graph == nil {
		return nil
	}
	return loader.graph.matrixResidency
}

func (loader *timeGraphMatrixLoader) fetchResident(ctx context.Context, fetch func() (pl.Matrix, bool, error)) (matrix pl.Matrix, partial bool, err error) {
	if loader.topology {
		select {
		case topologyBackendSlots <- struct{}{}:
			defer func() { <-topologyBackendSlots }()
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
	meter := loader.matrixMemory()
	if meter == nil {
		return fetch()
	}
	token := loader.matrixToken
	if token == "" {
		token = "current"
	}
	if err = meter.release(token); err != nil {
		return nil, false, err
	}
	if err = meter.begin(token, metadata.BackendResponseLimit(ctx)); err != nil {
		return nil, false, err
	}
	matrix, partial, err = fetch()
	if err == nil {
		err = meter.commit(token, matrix)
	}
	if err != nil {
		_ = meter.release(token)
		return nil, partial, err
	}
	return matrix, partial, nil
}
