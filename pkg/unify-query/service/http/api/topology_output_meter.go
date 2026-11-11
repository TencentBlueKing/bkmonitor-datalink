// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

// topologyEncodeMeter 累计一项实际编码的字节数，供 trace 和负载指标使用。
// 编码按行分块输出，字节数不是拒绝理由。
type topologyEncodeMeter struct {
	ctx  context.Context
	wire int
}

func (m *topologyEncodeMeter) wirePart(n int) error {
	if err := m.ctx.Err(); err != nil {
		return err
	}
	if n < 0 || n > math.MaxInt-m.wire {
		return fmt.Errorf("topology encoded size overflows")
	}
	m.wire += n
	return nil
}

// These conservative DTO bounds only select the compact fast path; they never
// reject an answer.
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
	if err := m.ctx.Err(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return encoded, m.ctx.Err()
}

// topologyCompactWholeItemBytes 是 compact 项整体编码的保守上界。更大的项按行分块
// 编码，最大的单次分配保持在块大小，避免整项缓冲及 json.Marshal 的复制随项增长。
// 两种编码输出相同的字节。
var topologyCompactWholeItemBytes int64 = 64 * topologyChunkTargetBytes

// Stop once whole-item encoding is no longer proved to fit. The fallback still
// returns the complete answer.
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

type topologyItemEncoder struct {
	ctx   context.Context
	meter *topologyEncodeMeter
	parts [][]byte
}

// own 追加本项独占的编码片段；片段编码后不再修改。
func (enc *topologyItemEncoder) own(raw []byte) error {
	if err := enc.meter.wirePart(len(raw)); err != nil {
		return err
	}
	enc.parts = append(enc.parts, raw)
	return nil
}
func (enc *topologyItemEncoder) raw(s string) error { return enc.own([]byte(s)) }
func (enc *topologyItemEncoder) value(v any) error {
	encoded, err := enc.meter.marshal(v)
	if err != nil {
		return err
	}
	return enc.own(encoded)
}

// marshalTopologyItem 把一项编码为有序片段，返回片段和实际编码字节数。
func marshalTopologyItem(ctx context.Context, payload any) ([][]byte, int, error) {
	m := &topologyEncodeMeter{ctx: ctx}
	enc := &topologyItemEncoder{ctx: ctx, meter: m}
	var err error
	switch item := payload.(type) {
	case cmdb.SharedTopologyResponseData:
		err = enc.snapshots(item, len(item.Snapshots), topologySnapshotSlice(item.Snapshots))
	case cmdb.CompactTopologyResponseData:
		err = enc.compact(item)
	default:
		err = fmt.Errorf("unsupported topology payload %T", payload)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, 0, err
	}
	return enc.parts, m.wire, nil
}

// marshalTopologySnapshotItem 编码 snapshots 格式的一项，帧由 source 逐帧交出、编码后即释放；
// item.Snapshots 不参与编码。字节与把全部帧放进 item.Snapshots 后编码相同。
func marshalTopologySnapshotItem(ctx context.Context, item cmdb.SharedTopologyResponseData, count int, source topologySnapshotSource) ([][]byte, int, error) {
	m := &topologyEncodeMeter{ctx: ctx}
	enc := &topologyItemEncoder{ctx: ctx, meter: m}
	err := enc.snapshots(item, count, source)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, 0, err
	}
	return enc.parts, m.wire, nil
}

func (enc *topologyItemEncoder) snapshots(item cmdb.SharedTopologyResponseData, count int, source topologySnapshotSource) error {
	m := enc.meter

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
	frames, err := marshalTopologySnapshotSource(m, count, source)
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

func (enc *topologyItemEncoder) compact(item cmdb.CompactTopologyResponseData) error {
	ctx, m := enc.ctx, enc.meter

	bound, err := topologyCompactEncoderBound(ctx, item, topologyCompactWholeItemBytes)
	if err != nil {
		return err
	}
	if bound <= topologyCompactWholeItemBytes {
		// Existing compact payloads are small: retain the standard fast encoder
		// when its conservative bound proves the whole item stays small.
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
		b := topologySnapshotChunkBuilder{meter: m, byHash: make(map[uint64][][]byte)}
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
