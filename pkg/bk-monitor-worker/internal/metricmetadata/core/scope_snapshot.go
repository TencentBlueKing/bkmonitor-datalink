// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package core

import (
	"encoding/binary"
	"fmt"
	"sort"
	"time"
)

// MetricState is a committed metric-to-value association.
type MetricState struct {
	ValueID     uint32
	Fingerprint string
}

// ScopeState is a committed snapshot. The processor owns its maps exclusively.
type ScopeState struct {
	ID       int64
	Key      ScopeKey
	Version  int
	LastSeen time.Time
	LoadedAt time.Time
	Metrics  map[string]MetricState
}

// Clone returns an independent snapshot for calculating pending changes.
func (s ScopeState) Clone() ScopeState {
	m := make(map[string]MetricState, len(s.Metrics))
	for k, v := range s.Metrics {
		m[k] = v
	}
	s.Metrics = m
	return s
}

// IDs returns the canonical, sorted set of member value IDs.
func (s ScopeState) IDs() []uint32 {
	ids := make([]uint32, 0, len(s.Metrics))
	for _, v := range s.Metrics {
		ids = append(ids, v.ValueID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Size estimates retained state memory for the cache usage metric.
func (s ScopeState) Size() int64 {
	n := int64(256 + len(s.Key.Hash))
	for name, v := range s.Metrics {
		n += int64(160 + len(name) + len(v.Fingerprint))
	}
	return n
}

// EncodeIDs encodes a sorted, unique sequence in Metadata's uint32 little-endian format.
func EncodeIDs(ids []uint32) ([]byte, error) {
	b := make([]byte, len(ids)*4)
	for i, id := range ids {
		if id == 0 || (i > 0 && id <= ids[i-1]) {
			return nil, fmt.Errorf("value IDs must be positive, unique and sorted")
		}
		binary.LittleEndian.PutUint32(b[i*4:], id)
	}
	return b, nil
}

// DecodeIDs validates persisted member data instead of treating corruption as an empty scope.
func DecodeIDs(b []byte, count int) ([]uint32, error) {
	if len(b)%4 != 0 || count < 0 || len(b)/4 != count {
		return nil, fmt.Errorf("invalid value ID blob length/count")
	}
	ids := make([]uint32, count)
	for i := range ids {
		ids[i] = binary.LittleEndian.Uint32(b[i*4:])
		if ids[i] == 0 || (i > 0 && ids[i] <= ids[i-1]) {
			return nil, fmt.Errorf("value IDs must be positive, unique and sorted")
		}
	}
	return ids, nil
}

// KeepLast preserves the final record per metadata key without comparing offsets across partitions.
func KeepLast(records []Record) []Record {
	last := make(map[MetadataKey]int, len(records))
	for i, r := range records {
		last[r.Key()] = i
	}
	result := make([]Record, 0, len(last))
	for i, r := range records {
		if last[r.Key()] == i {
			result = append(result, r)
		}
	}
	return result
}

// ReferenceDelta compares whole member sets so replacements do not change member counts.
func ReferenceDelta(before, after ScopeState) map[uint32]int {
	delta := make(map[uint32]int)
	for _, v := range before.Metrics {
		delta[v.ValueID]--
	}
	for _, v := range after.Metrics {
		delta[v.ValueID]++
	}
	for id, d := range delta {
		if d == 0 {
			delete(delta, id)
		}
	}
	return delta
}
