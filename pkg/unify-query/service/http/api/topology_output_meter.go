// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
)

type (
	topologyEncodeMeterKey struct{}
	topologyEncodeMeter    struct {
		ctx                                               context.Context
		limit, wire                                       int
		prior, owned, working, cached, scratch, container int64
		reserved                                          int64
	}
)

func topologyMeter(ctx context.Context) *topologyEncodeMeter {
	m, _ := ctx.Value(topologyEncodeMeterKey{}).(*topologyEncodeMeter)
	return m
}

func (m *topologyEncodeMeter) checkWire(n int) error {
	if m == nil {
		return nil
	}
	if err := m.ctx.Err(); err != nil {
		return err
	}
	if n < 0 || n > math.MaxInt-m.wire {
		return fmt.Errorf("topology encoded size overflows")
	}
	if n > m.limit-m.wire {
		return &v1beta3.ResultLimitError{Reason: "max_topology_output_bytes", Count: m.wire + n, Limit: m.limit}
	}
	return nil
}

func (m *topologyEncodeMeter) wirePart(n int) error {
	if m == nil {
		return nil
	}
	if err := m.checkWire(n); err != nil {
		return err
	}
	m.wire += n
	return nil
}

func (m *topologyEncodeMeter) reserve() error {
	if m == nil {
		return nil
	}
	if err := m.ctx.Err(); err != nil {
		return err
	}
	// Owned chunks and prior items stay live. Working/cached/scratch are current
	// encoder allocations; metadata coefficients cover Go map and slice growth.
	want := m.prior + m.owned + m.working + m.cached + m.scratch + m.container
	const block int64 = 4 * 1024 * 1024
	if want < 0 || want > math.MaxInt64-block+1 {
		return fmt.Errorf("topology encoder memory estimate overflows")
	}
	want = (want + block - 1) / block * block
	if want > m.reserved {
		if err := v1beta3.ReserveTopologyStage(m.ctx, "encoded", want); err != nil {
			return err
		}
		m.reserved = want
	}
	return nil
}

func (m *topologyEncodeMeter) retain(n int64) error {
	if m == nil {
		return nil
	}
	m.owned += n
	return m.reserve()
}

func (m *topologyEncodeMeter) cache(n int64) error {
	if m == nil {
		return nil
	}
	m.cached += n
	return m.reserve()
}

// These DTO bounds reserve encoder scratch and select the compact fast path.
// They never reject an answer by treating a conservative bound as wire bytes.
type topologyResponseHeader struct {
	Code   int    `json:"code"`
	Start  int64  `json:"start_time"`
	End    int64  `json:"end_time"`
	Step   string `json:"step"`
	Points int    `json:"point_count"`
}
type topologyCompactResponseHeader struct {
	Code   int    `json:"code"`
	Start  int64  `json:"start_time"`
	End    int64  `json:"end_time"`
	Step   string `json:"step"`
	Points int    `json:"point_count"`
	Format string `json:"response_format"`
}
type topologySnapshotHeader struct {
	Timestamp int64 `json:"timestamp"`
}
type topologySnapshotTrailer struct {
	Partial bool   `json:"partial"`
	Reason  string `json:"partial_reason,omitempty"`
}
type topologyCompactHeader struct {
	Version int     `json:"version"`
	Times   []int64 `json:"timestamps"`
}
type topologyCompactTail struct {
	Nodes int `json:"node_occurrences"`
	Edges int `json:"edge_occurrences"`
}

func topologyJSONStringBound(s string) int64 { return 2 + 6*int64(len(s)) }
func topologyMatcherWireBound(info cmdb.Matcher) int64 {
	n := int64(4)
	for key, value := range info {
		n += topologyJSONStringBound(key) + topologyJSONStringBound(value) + 2
	}
	return n
}

func topologyStringSliceWireBound(words []string) int64 {
	n := int64(4)
	for _, word := range words {
		n += topologyJSONStringBound(word) + 1
	}
	return n
}

func topologyDTOEncoderBound(value any) (int64, error) {
	switch v := value.(type) {
	case string:
		return topologyJSONStringBound(v), nil
	case topologyResponseHeader:
		return 256 + topologyJSONStringBound(v.Step), nil
	case topologyCompactResponseHeader:
		return 320 + topologyJSONStringBound(v.Step) + topologyJSONStringBound(v.Format), nil
	case topologySnapshotHeader:
		return 64, nil
	case topologySnapshotTrailer:
		return 64 + topologyJSONStringBound(v.Reason), nil
	case topologyCompactHeader:
		return 128 + 21*int64(len(v.Times)), nil
	case topologyCompactTail:
		return 128, nil
	case cmdb.SharedTopologyNode:
		return 128 + topologyJSONStringBound(string(v.ResourceType)) + topologyMatcherWireBound(v.Dimensions), nil
	case cmdb.SharedTopologyEdge:
		return 192 + topologyJSONStringBound(v.RelationType) + topologyJSONStringBound(v.MetricName) + topologyJSONStringBound(v.Category) + topologyJSONStringBound(v.Direction), nil
	case cmdb.CompactTopologyNode:
		return 128 + topologyJSONStringBound(v.ID) + topologyJSONStringBound(string(v.ResourceType)) + topologyMatcherWireBound(v.Dimensions) + topologyStringSliceWireBound(v.Mask), nil
	case cmdb.CompactTopologyEdge:
		return 192 + topologyJSONStringBound(v.Source) + topologyJSONStringBound(v.Target) + topologyJSONStringBound(v.RelationType) + topologyJSONStringBound(v.MetricName) + topologyJSONStringBound(v.Category) + topologyJSONStringBound(v.Direction) + topologyStringSliceWireBound(v.Mask), nil
	case []cmdb.CompactTopologyPartial:
		n := int64(4)
		for _, p := range v {
			n += 64 + topologyJSONStringBound(p.Reason)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("unsupported topology encoder DTO %T", value)
	}
}

func (m *topologyEncodeMeter) marshal(value any) ([]byte, error) {
	if m == nil {
		return json.Marshal(value)
	}
	if err := m.ctx.Err(); err != nil {
		return nil, err
	}
	bound, err := topologyDTOEncoderBound(value)
	if err != nil {
		return nil, err
	}
	if bound < 0 || bound > (math.MaxInt64-3)/3 {
		return nil, fmt.Errorf("topology encoder memory estimate overflows")
	}
	m.scratch = 3 * (bound + 1)
	if err = m.reserve(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if err = m.checkWire(len(encoded)); err != nil {
		return nil, err
	}
	return encoded, m.ctx.Err()
}

// Stop once whole-item encoding is no longer proved to fit. The fallback still
// returns the complete answer, with resource reservations and actual byte checks.
func topologyCompactEncoderBound(ctx context.Context, item cmdb.CompactTopologyResponseData, stop int64) (int64, error) {
	n := int64(1024) + topologyJSONStringBound(item.Step) + topologyJSONStringBound(item.ResponseFormat) + topologyJSONStringBound(item.Message)
	if item.Compact == nil {
		return n, nil
	}
	c := item.Compact
	n += 21 * int64(len(c.Timestamps))
	for _, node := range c.Nodes {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		v, _ := topologyDTOEncoderBound(node)
		n += v
		if n > stop {
			return n, nil
		}
	}
	for _, edge := range c.Edges {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		v, _ := topologyDTOEncoderBound(edge)
		n += v
		if n > stop {
			return n, nil
		}
	}
	v, _ := topologyDTOEncoderBound(c.Partial)
	return n + v, nil
}

type topologyEncodedOwnership struct {
	reserved, materialized int64
}

func topologyOwnedParts(parts [][]byte) topologyEncodedOwnership {
	// The request owns these immutable buffers. Unused capacity stays pending;
	// only initialized slice headers and written bytes are materialized credit.
	seen := make(map[*byte]bool, len(parts))
	owned := topologyEncodedOwnership{reserved: int64(cap(parts)) * int64(unsafe.Sizeof([]byte{})), materialized: int64(len(parts)) * int64(unsafe.Sizeof([]byte{}))}
	for _, p := range parts {
		if len(p) > 0 && !seen[unsafe.SliceData(p)] {
			seen[unsafe.SliceData(p)] = true
			owned.reserved += int64(cap(p))
			owned.materialized += int64(len(p))
		}
	}
	return owned
}

type topologyBoundedEncoder struct {
	ctx   context.Context
	meter *topologyEncodeMeter
	parts [][]byte
}

func (enc *topologyBoundedEncoder) own(raw []byte) error {
	if err := enc.meter.wirePart(len(raw)); err != nil {
		return err
	}
	if err := enc.meter.retain(int64(len(raw))*2 + int64(unsafe.Sizeof([]byte{}))); err != nil {
		return err
	}
	owned := make([]byte, len(raw))
	copy(owned, raw)
	enc.parts = append(enc.parts, owned)
	return nil
}
func (enc *topologyBoundedEncoder) raw(s string) error { return enc.own([]byte(s)) }
func (enc *topologyBoundedEncoder) value(v any) error {
	encoded, err := enc.meter.marshal(v)
	if err != nil {
		return err
	}
	return enc.own(encoded)
}

func marshalTopologyBounded(ctx context.Context, payload any, remaining int, priorOwned topologyEncodedOwnership) ([][]byte, int, topologyEncodedOwnership, error) {
	m := &topologyEncodeMeter{ctx: ctx, limit: max(0, remaining), prior: priorOwned.reserved}
	ctx = context.WithValue(ctx, topologyEncodeMeterKey{}, m)
	enc := &topologyBoundedEncoder{ctx: ctx, meter: m}
	var err error
	switch item := payload.(type) {
	case cmdb.SharedTopologyResponseData:
		err = enc.snapshots(item)
	case cmdb.CompactTopologyResponseData:
		err = enc.compact(item)
	default:
		err = fmt.Errorf("unsupported bounded topology payload %T", payload)
	}
	if err != nil {
		return nil, 0, topologyEncodedOwnership{}, err
	}
	owned := topologyOwnedParts(enc.parts)
	// Prior encoded items and the current DTO remain live in separate stages.
	if err := v1beta3.ReserveTopologyStage(ctx, "encoded", priorOwned.reserved+owned.reserved); err != nil {
		return nil, 0, topologyEncodedOwnership{}, err
	}
	if err := v1beta3.CommitTopologyStage(ctx, "encoded", priorOwned.materialized+owned.materialized); err != nil {
		return nil, 0, topologyEncodedOwnership{}, err
	}
	return enc.parts, m.wire, owned, ctx.Err()
}

func (enc *topologyBoundedEncoder) snapshots(item cmdb.SharedTopologyResponseData) error {
	ctx, m := enc.ctx, enc.meter

	header, err := m.marshal(topologyResponseHeader{item.Code, item.StartTime, item.EndTime, item.Step, item.PointCount})
	if err != nil {
		return err
	}
	if err = enc.own(header[:len(header)-1]); err == nil {
		err = enc.raw(`,"snapshots":[`)
	}
	if err != nil {
		return err
	}
	frames, err := marshalTopologySnapshotFrames(ctx, item.Snapshots, false)
	if err != nil {
		return err
	}
	enc.parts = append(enc.parts, frames...)
	if err = enc.raw("]"); err != nil {
		return err
	}
	if item.Message != "" {
		if err = enc.raw(`,"message":`); err == nil {
			err = enc.value(item.Message)
		}
		if err != nil {
			return err
		}
	}
	if err = enc.raw("}"); err != nil {
		return err
	}
	return nil
}

func (enc *topologyBoundedEncoder) compact(item cmdb.CompactTopologyResponseData) error {
	ctx, m := enc.ctx, enc.meter

	bound, err := topologyCompactEncoderBound(ctx, item, int64(m.limit-m.wire))
	if err != nil {
		return err
	}
	if bound <= int64(m.limit-m.wire) {
		// Existing compact payloads are small: retain the standard fast encoder
		// when its conservative bound already proves it fits the actual quota.
		m.scratch = 3 * (bound + 1)
		if err = m.reserve(); err != nil {
			return err
		}
		encoded, e := json.Marshal(item)
		if e != nil {
			return e
		}
		if e = m.wirePart(len(encoded)); e != nil {
			return e
		}
		enc.parts = append(enc.parts, encoded)
		return nil
	}
	header, err := m.marshal(topologyCompactResponseHeader{item.Code, item.StartTime, item.EndTime, item.Step, item.PointCount, item.ResponseFormat})
	if err != nil {
		return err
	}
	if err = enc.own(header[:len(header)-1]); err == nil {
		err = enc.raw(`,"compact":`)
	}
	if err != nil {
		return err
	}
	if item.Compact == nil {
		if err = enc.raw("null"); err != nil {
			return err
		}
	} else {
		c := item.Compact
		b := topologySnapshotChunkBuilder{ctx: ctx, meter: m, byHash: make(map[uint64][][]byte)}
		appendValue := func(v any) error {
			encoded, e := m.marshal(v)
			if e != nil {
				return e
			}
			return b.append(encoded, 1)
		}
		text := func(s string) error { return b.append([]byte(s), 1) }
		h, e := m.marshal(topologyCompactHeader{c.Version, c.Timestamps})
		if e != nil {
			return e
		}
		if e = b.append(h[:len(h)-1], 1); e == nil {
			e = text(`,"nodes":[`)
		}
		if e != nil {
			return e
		}
		for i, n := range c.Nodes {
			if i > 0 {
				if e = text(","); e != nil {
					return e
				}
			}
			if e = appendValue(n); e != nil {
				return e
			}
		}
		if e = text(`],"edges":[`); e != nil {
			return e
		}
		for i, n := range c.Edges {
			if i > 0 {
				if e = text(","); e != nil {
					return e
				}
			}
			if e = appendValue(n); e != nil {
				return e
			}
		}
		if e = text(`],"partial":`); e == nil {
			e = appendValue(c.Partial)
		}
		if e != nil {
			return e
		}
		tail, e := m.marshal(topologyCompactTail{c.NodeOccurrences, c.EdgeOccurrences})
		if e != nil {
			return e
		}
		if e = text(","); e == nil {
			e = b.append(tail[1:], 1)
		}
		if e == nil {
			e = b.flush()
		}
		if e != nil {
			return e
		}
		enc.parts = append(enc.parts, b.parts...)
	}
	if item.Message != "" {
		if err = enc.raw(`,"message":`); err == nil {
			err = enc.value(item.Message)
		}
		if err != nil {
			return err
		}
	}
	if err = enc.raw("}"); err != nil {
		return err
	}
	return nil
}
