// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Code copy from VictoriaMetrics

package serieslimiter

import (
	"sync/atomic"
	"unsafe"

	"github.com/cespare/xxhash/v2"
)

const (
	hashesCount = 4
	bitsPerItem = 16
)

type filter struct {
	maxItems int
	bits     []uint64
}

func newFilter(maxItems int) *filter {
	bitsCount := maxItems * bitsPerItem
	bits := make([]uint64, (bitsCount+63)/64)
	return &filter{
		maxItems: maxItems,
		bits:     bits,
	}
}

// Reset resets f to initial state.
//
// It is expected no other goroutines call f methods during Reset call.
func (f *filter) Reset() {
	bits := f.bits
	for i := range bits {
		bits[i] = 0
	}
}

// Has checks whether h presents in f.
//
// Has can be called from concurrent goroutines.
func (f *filter) Has(h uint64) bool {
	bits := f.bits
	maxBits := uint64(len(bits)) * 64
	bp := (*[8]byte)(unsafe.Pointer(&h))
	b := bp[:]
	for i := 0; i < hashesCount; i++ {
		hi := xxhash.Sum64(b)
		h++
		idx := hi % maxBits
		i := idx / 64
		j := idx % 64
		mask := uint64(1) << j
		w := atomic.LoadUint64(&bits[i])
		if (w & mask) == 0 {
			return false
		}
	}
	return true
}

// Add adds h to f.
//
// True is returned if h was missing in f.
//
// Add can be called from concurrent goroutines.
// If the same h is added to f from concurrent goroutines, then both goroutines may return true.
func (f *filter) Add(h uint64) bool {
	bits := f.bits
	maxBits := uint64(len(bits)) * 64
	bp := (*[8]byte)(unsafe.Pointer(&h))
	b := bp[:]
	isNew := false
	for i := 0; i < hashesCount; i++ {
		hi := xxhash.Sum64(b)
		h++
		idx := hi % maxBits
		i := idx / 64
		j := idx % 64
		mask := uint64(1) << j
		w := atomic.LoadUint64(&bits[i])
		for (w & mask) == 0 {
			wNew := w | mask
			if atomic.CompareAndSwapUint64(&bits[i], w, wNew) {
				isNew = true
				break
			}
			w = atomic.LoadUint64(&bits[i])
		}
	}
	return isNew
}
