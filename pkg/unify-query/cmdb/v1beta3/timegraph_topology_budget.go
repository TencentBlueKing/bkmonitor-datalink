// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

var topologyAdmission struct {
	sync.Mutex
	active       int
	reserved     int64
	materialized int64
	waiters      []*topologyWaiter
	changed      chan struct{}
}

type topologyAdmissionKey struct{}

type topologyLease struct {
	reserved int64
	stages   map[string]int64
	// Absolute lower bounds of already materialized, request-owned live storage.
	// Borrowed storage and conservative reservation multipliers cannot be credited.
	materialized map[string]int64
	released     bool
	yolo         bool
}

// AcquireSharedTopology covers the complete HTTP response lifetime. A normal
// request may wait for an execution slot within the same request deadline.
// Generic/model callers reserve nothing while waiting; the HTTP variant may
// retain only its bounded request body. Nested calls reuse the admitted lease.
func AcquireSharedTopology(ctx context.Context) (admittedCtx context.Context, releaseFn func(), err error) {
	return acquireSharedTopology(ctx, nil)
}

// AcquireSharedTopologyWithQueuedPreparation lets HTTP drain its bounded body
// after obtaining a FIFO queue position, so net/http can observe peer closure.
// Preparation is called only when queued and runs without the admission lock.
// It may reserve request-buffer memory but must not execute model/graph work.
func AcquireSharedTopologyWithQueuedPreparation(ctx context.Context, prepare func(context.Context) error) (context.Context, func(), error) {
	return acquireSharedTopology(ctx, prepare)
}

func acquireSharedTopology(ctx context.Context, prepare func(context.Context) error) (admittedCtx context.Context, releaseFn func(), err error) {
	started := time.Now()
	_, span := trace.NewSpan(ctx, "timegraph-admission")
	defer finishTimeGraphStage(ctx, span, "admission", started, &err)
	if err := ctx.Err(); err != nil {
		return ctx, nil, err
	}
	if admitted, _ := ctx.Value(topologyAdmissionKey{}).(*topologyLease); admitted != nil {
		topologyAdmission.Lock()
		released := admitted.released
		topologyAdmission.Unlock()
		if released {
			return ctx, nil, context.Canceled
		}
		span.Set("admission-reused", true)
		return ctx, func() {}, nil
	}
	lease := &topologyLease{stages: make(map[string]int64), materialized: make(map[string]int64), yolo: yoloMode}
	cancel := func() {}
	if !lease.yolo {
		// The existing model limit begins before admission. A shorter parent
		// deadline remains authoritative, so waiting cannot restart its clock.
		ctx, cancel = context.WithTimeout(ctx, timeGraphQueryTimeout)
	}
	// A queued preparation may own only request buffers before becoming active.
	ctx = context.WithValue(ctx, topologyAdmissionKey{}, lease)
	defer func() {
		if err != nil {
			cancel()
		}
	}()
	topologyAdmission.Lock()
	defer topologyAdmission.Unlock()
	defer func() {
		metric.CMDBTopologyAdmissionSet(topologyAdmission.active)
		metric.CMDBTopologyReservedSet(topologyAdmission.reserved)
		span.Set("admission-active", topologyAdmission.active)
		span.Set("admission-reserved-bytes", topologyAdmission.reserved)
		span.Set("admission-waiters", len(topologyAdmission.waiters))
	}()
	if !lease.yolo {
		limit := effectiveTimeGraphLimit(MaxSharedTopologyConcurrent, 8)
		if err = waitTopologySlotLocked(ctx, limit, span, prepare); err != nil {
			lease.releaseMemoryLocked()
			return ctx, nil, err
		}
		// Empty admission owns no payload. HTTP reserves its bounded request
		// buffers before reading; Model stages reserve before allocating. The
		// execution limit bounds the small lease/timer bookkeeping itself.
	}
	topologyAdmission.active++
	notifyTopologyWaitersLocked()
	var once sync.Once
	release := func() {
		once.Do(func() {
			defer cancel()
			_, releaseSpan := trace.NewSpan(ctx, "timegraph-release-admission")
			var releaseErr error
			defer finishTimeGraphStage(ctx, releaseSpan, "admission-release", time.Now(), &releaseErr)
			topologyAdmission.Lock()
			defer topologyAdmission.Unlock()
			topologyAdmission.active--
			lease.releaseMemoryLocked()
			notifyTopologyWaitersLocked()
			releaseSpan.Set("admission-active", topologyAdmission.active)
			metric.CMDBTopologyAdmissionSet(topologyAdmission.active)
			metric.CMDBTopologyReservedSet(topologyAdmission.reserved)
		})
	}
	return ctx, release, nil
}

// Called with the admission lock on either failed preparation/wait or release.
func (lease *topologyLease) releaseMemoryLocked() {
	topologyAdmission.reserved -= lease.reserved
	for _, bytes := range lease.materialized {
		topologyAdmission.materialized -= bytes
	}
	lease.reserved = 0
	lease.stages = nil
	lease.materialized = nil
	lease.released = true
	metric.CMDBTopologyMaterializedSet(topologyAdmission.materialized)
}

// reserve requires the admission lock. The static reservation envelope still
// applies to all live stages. For measured headroom, only confirmed materialized
// lower bounds may be credited; all remaining estimates stay pending. This is
// an estimate-based admission mechanism, not an absolute RSS/OOM guarantee.
func (lease *topologyLease) reserve(want int64, span *trace.Span) error {
	delta := want - lease.reserved
	if delta > 0 {
		limit := int64(effectiveTimeGraphLimit(MaxSharedTopologyReservedBytes, 4*1024*1024*1024))
		if delta > limit-topologyAdmission.reserved {
			return &ResultLimitError{Reason: "max_topology_reserved_bytes", Count: int(topologyAdmission.reserved + delta), Limit: int(limit)}
		}
		available, known := topologyMemoryHeadroom()
		span.Set("memory-headroom-known", known)
		span.Set("memory-headroom-bytes", available)
		if known {
			margin := int64(effectiveTimeGraphLimit(SharedTopologyMemoryHeadroom, 512*1024*1024))
			usable := max(int64(0), available-margin)
			pending := topologyAdmission.reserved - topologyAdmission.materialized
			span.Set("memory-pending-bytes", pending)
			span.Set("memory-materialized-bytes", topologyAdmission.materialized)
			if delta > usable-pending {
				return &ResultLimitError{Reason: "max_topology_memory_headroom", Count: int(pending + delta), Limit: int(usable)}
			}
		}
	}
	topologyAdmission.reserved += delta
	lease.reserved = want
	return nil
}

// ReserveTopologyStage updates live stage estimates before allocation. Growing a
// live stage preserves its confirmed lower bound. A replacement must first clear
// its previous confirmation. Zero releases both reservation and confirmation,
// including after cancellation; shrinking clamps the confirmation conservatively.
func ReserveTopologyStage(ctx context.Context, stage string, bytes int64) (err error) {
	lease, _ := ctx.Value(topologyAdmissionKey{}).(*topologyLease)
	if lease == nil || lease.yolo {
		return nil
	}
	if bytes < 0 {
		return fmt.Errorf("negative or overflowing topology memory estimate for %s", stage)
	}
	if bytes > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	_, span := trace.NewSpan(ctx, "timegraph-reserve-memory")
	defer func() {
		SetTimeGraphLimitTrace(span, err)
		span.End(&err)
	}()
	span.Set("memory-stage", stage)
	span.Set("stage-estimated-bytes", bytes)
	topologyAdmission.Lock()
	defer topologyAdmission.Unlock()
	if lease.released {
		return context.Canceled
	}
	want := bytes
	for name, size := range lease.stages {
		if name != stage {
			if size > math.MaxInt64-want {
				return &ResultLimitError{Reason: "max_topology_reserved_bytes", Count: math.MaxInt, Limit: effectiveTimeGraphLimit(MaxSharedTopologyReservedBytes, 4*1024*1024*1024)}
			}
			want += size
		}
	}
	if err := lease.reserve(want, span); err != nil {
		return err
	}
	lease.stages[stage] = bytes
	confirmed := min(lease.materialized[stage], bytes)
	topologyAdmission.materialized += confirmed - lease.materialized[stage]
	lease.materialized[stage] = confirmed
	metric.CMDBTopologyMaterializedSet(topologyAdmission.materialized)
	span.Set("request-reserved-bytes", lease.reserved)
	metric.CMDBTopologyReservedSet(topologyAdmission.reserved)
	return nil
}

// CommitTopologyStage confirms an absolute lower bound of newly owned storage
// already allocated and still live in this stage, never the estimate multiplier.
// Storage borrowed from Matrix, another stage, or a cache must not be credited.
// Clear before replacing or transferring ownership; the destination can then
// confirm its own live lower bound. Unknown footprint uses zero and stays pending.
func CommitTopologyStage(ctx context.Context, stage string, materializedLowerBoundBytes int64) error {
	lease, _ := ctx.Value(topologyAdmissionKey{}).(*topologyLease)
	if lease == nil || lease.yolo {
		return nil
	}
	if materializedLowerBoundBytes < 0 {
		return fmt.Errorf("negative materialized topology memory for %s", stage)
	}
	if materializedLowerBoundBytes > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	topologyAdmission.Lock()
	defer topologyAdmission.Unlock()
	if lease.released {
		return context.Canceled
	}
	if materializedLowerBoundBytes > lease.stages[stage] {
		return fmt.Errorf("materialized topology memory for %s exceeds stage reservation", stage)
	}
	topologyAdmission.materialized += materializedLowerBoundBytes - lease.materialized[stage]
	lease.materialized[stage] = materializedLowerBoundBytes
	metric.CMDBTopologyMaterializedSet(topologyAdmission.materialized)
	return nil
}

// TopologyOutputByteLimit 是 HTTP 批次实际编码字节预算；DTO 物化使用内存预约。
func TopologyOutputByteLimit() int {
	return effectiveTimeGraphLimit(MaxSharedTopologyOutputBytes, 64*1024*1024)
}

type topologyOutputBudget struct {
	retainedStrings map[topologyStringIdentity]struct{}
	ctx             context.Context
	reservedBytes   int64
	elements        int
	bytes           int64
}

// Model allocation and wire size are different budgets. This budget reserves
// DTO backing storage before allocation; HTTP encoding owns the wire cap.
func newTopologyOutputBudget(grid TopologyGrid, _ map[int64]string) *topologyOutputBudget {
	return &topologyOutputBudget{bytes: int64(len(grid.Timestamps)) * int64(unsafe.Sizeof(cmdb.SharedTopologySnapshot{}))}
}

func (b *topologyOutputBudget) checkBytes(size int64) error {
	if size < 0 || size > math.MaxInt64-b.bytes {
		return fmt.Errorf("overflowing topology DTO memory estimate")
	}
	return b.reserve(b.bytes + size)
}

func (b *topologyOutputBudget) reserve(want int64) error {
	if b.ctx == nil {
		return nil
	}
	if err := b.ctx.Err(); err != nil {
		return err
	}
	const chunk int64 = 4 * 1024 * 1024
	if want > math.MaxInt64-chunk+1 {
		return fmt.Errorf("overflowing topology DTO memory reservation")
	}
	want = (want + chunk - 1) / chunk * chunk
	if want > b.reservedBytes {
		if err := ReserveTopologyStage(b.ctx, "output", want); err != nil {
			return err
		}
		b.reservedBytes = want
	}
	return nil
}
func (b *topologyOutputBudget) add(size int64) error { return b.addCharge(size, 1) }
func (b *topologyOutputBudget) addCharge(size int64, count int) error {
	if size < 0 || count < 0 || size > math.MaxInt64-b.bytes || count > math.MaxInt-b.elements {
		return fmt.Errorf("overflowing topology DTO charge")
	}
	if err := b.reserve(b.bytes + size); err != nil {
		return err
	}
	b.bytes += size
	b.elements += count
	return nil
}

// Map entries borrow immutable string backing data; only cloned table/container
// storage belongs to output. 64B map header + 8-slot groups at 7/8 load is an
// explicit Go 1.26 calibration estimate, not an exact allocator/OOM guarantee.
func topologyMatcherMemory(entries int) int64 {
	if entries == 0 {
		return 64
	}
	groups := int64(1)
	for groups*7 < int64(entries) {
		groups *= 2
	}
	return 64 + groups*(16+8*int64(unsafe.Sizeof(struct{ Key, Value string }{})))
}

func topologyMatcherOwnedLowerBound(info cmdb.Matcher) int64 {
	return int64(len(info)) * int64(unsafe.Sizeof(struct{ Key, Value string }{}))
}

// This borrowed view is used only while the TimeGraph read lock prevents Clean;
// the emitted DTO still clones its map before returning to callers.
func (q *TimeGraph) topologyBorrowedNodeInfo(id uint64) cmdb.Matcher {
	q.nodeBuilder.lock.RLock()
	defer q.nodeBuilder.lock.RUnlock()
	return q.nodeBuilder.info[id]
}

func topologySnapshotContainers(nodes, edges []int) int64 {
	bytes := int64(0)
	for i := range nodes {
		bytes += int64(nodes[i])*int64(unsafe.Sizeof(cmdb.SharedTopologyNode{})) + int64(edges[i])*int64(unsafe.Sizeof(cmdb.SharedTopologyEdge{}))
	}
	return bytes
}

// Skip DTO accounting entirely for requests admitted in YOLO mode.
func commitTopologyOutput(ctx context.Context, lowerBound func() int64) error {
	lease, _ := ctx.Value(topologyAdmissionKey{}).(*topologyLease)
	if lease == nil || lease.yolo {
		return nil
	}
	return CommitTopologyStage(ctx, "output", lowerBound())
}

func topologySnapshotOwnedLowerBound(frames []cmdb.SharedTopologySnapshot) int64 {
	size := int64(len(frames)) * int64(unsafe.Sizeof(cmdb.SharedTopologySnapshot{}))
	for _, frame := range frames {
		size += int64(len(frame.Nodes))*int64(unsafe.Sizeof(cmdb.SharedTopologyNode{})) + int64(len(frame.Edges))*int64(unsafe.Sizeof(cmdb.SharedTopologyEdge{}))
		for _, node := range frame.Nodes {
			size += topologyMatcherOwnedLowerBound(node.Dimensions)
		}
	}
	return size
}

func topologyCompactOwnedLowerBound(result *cmdb.CompactTopology) int64 {
	// Commit only initialized DTO and map slots. String storage and unused slice
	// capacity stay reserved/pending; no additional dictionary is built merely
	// to claim a few bytes of ID/mask string credit.
	size := int64(unsafe.Sizeof(*result)) + int64(len(result.Timestamps))*8 + int64(len(result.Nodes))*int64(unsafe.Sizeof(cmdb.CompactTopologyNode{})) + int64(len(result.Edges))*int64(unsafe.Sizeof(cmdb.CompactTopologyEdge{})) + int64(len(result.Partial))*int64(unsafe.Sizeof(cmdb.CompactTopologyPartial{}))
	for _, n := range result.Nodes {
		size += topologyMatcherOwnedLowerBound(n.Dimensions) + int64(len(n.Mask))*int64(unsafe.Sizeof(""))
	}
	for _, e := range result.Edges {
		size += int64(len(e.Mask)) * int64(unsafe.Sizeof(""))
	}
	return size
}

// Pointer+length preserves distinct backing allocations and safely over-counts
// overlapping substrings. These borrowed bytes remain reserved after graph
// cleanup; they are not committed as newly allocated output storage.
type topologyStringIdentity struct {
	data   *byte
	length int
}

func (b *topologyOutputBudget) retainString(s string) error {
	if yoloMode || len(s) == 0 {
		return nil
	}
	id := topologyStringIdentity{unsafe.StringData(s), len(s)}
	if _, ok := b.retainedStrings[id]; ok {
		return nil
	}
	if err := b.addCharge(int64(len(s))+64, 0); err != nil {
		return err
	}
	if b.retainedStrings == nil {
		b.retainedStrings = make(map[topologyStringIdentity]struct{})
	}
	b.retainedStrings[id] = struct{}{}
	return nil
}

func (b *topologyOutputBudget) retainMatcher(resource cmdb.Resource, info cmdb.Matcher) error {
	if yoloMode {
		return nil
	}
	if err := b.retainString(string(resource)); err != nil {
		return err
	}
	for key, value := range info {
		if err := b.retainString(key); err != nil {
			return err
		}
		if err := b.retainString(value); err != nil {
			return err
		}
	}
	return nil
}

func (b *topologyOutputBudget) retainEdge(edge timeGraphTopologyEdgeKey) error {
	if yoloMode {
		return nil
	}
	for _, s := range []string{edge.relation.relationType, edge.relation.metricName, edge.relation.category, edge.relation.direction} {
		if err := b.retainString(s); err != nil {
			return err
		}
	}
	return nil
}
