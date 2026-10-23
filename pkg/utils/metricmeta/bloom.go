// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metricmeta

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
)

type fingerprint [32]byte

func digest(e Entry) fingerprint {
	h := sha256.New()
	var b [8]byte
	writeString := func(value string) {
		binary.LittleEndian.PutUint64(b[:], uint64(len(value)))
		_, _ = h.Write(b[:])
		_, _ = h.Write([]byte(value))
	}
	binary.LittleEndian.PutUint64(b[:], uint64(e.Key.DataID))
	_, _ = h.Write(b[:])
	for _, value := range []string{
		string(e.Key.ScopeType), e.Key.Scope, e.Key.Metric, e.Value.Name,
		e.Value.Unit, e.Value.Description, e.Value.Type, e.Value.OriginalType, e.Value.Temporality, e.Value.Usage,
	} {
		writeString(value)
	}
	binary.LittleEndian.PutUint64(b[:], uint64(e.Value.Interval))
	_, _ = h.Write(b[:])
	_, _ = h.Write([]byte{e.Value.Present})
	var result fingerprint
	h.Sum(result[:0])
	return result
}

type bloomFilter struct {
	bits  []uint64
	count int
	k     uint64
}

func newBloom(capacity int, probability float64) bloomFilter {
	m := math.Ceil(-float64(capacity) * math.Log(probability) / (math.Ln2 * math.Ln2))
	k := uint64(math.Ceil(m / float64(capacity) * math.Ln2))
	return bloomFilter{bits: make([]uint64, (uint64(m)+63)/64), k: k}
}

func (b *bloomFilter) contains(d fingerprint) bool {
	a, step := binary.LittleEndian.Uint64(d[:8]), binary.LittleEndian.Uint64(d[8:16])|1
	m := uint64(len(b.bits)) * 64
	for i := uint64(0); i < b.k; i++ {
		p := (a + i*step) % m
		if b.bits[p/64]&(uint64(1)<<(p%64)) == 0 {
			return false
		}
	}
	return true
}

func (b *bloomFilter) add(d fingerprint) {
	a, step := binary.LittleEndian.Uint64(d[:8]), binary.LittleEndian.Uint64(d[8:16])|1
	m := uint64(len(b.bits)) * 64
	for i := uint64(0); i < b.k; i++ {
		p := (a + i*step) % m
		b.bits[p/64] |= uint64(1) << (p % 64)
	}
	b.count++
}

func (b *bloomFilter) reset() {
	clear(b.bits)
	b.count = 0
}
