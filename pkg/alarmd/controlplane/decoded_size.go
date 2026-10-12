// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"math"
	"reflect"
	"sync/atomic"
	"unsafe"
)

// decodeSampleEvery is how many objects the object cache stores for each one
// it sizes: the running process's reading of the decoded charge.
const decodeSampleEvery = 64

// DecodedObjectReading is the object cache's sampled reading of what its
// objects take decoded against the stored bytes it counts them by: Samples
// objects sized so far, and the Last and Max ratio. The size is structural
// (structuralBytes), a lower bound on what an object retains; the charge
// (decodedObjectBytes) is set from TestDecodedQueryGroupObjectHeapFootprint,
// which measures retained heap. A Max near or past the charge is the
// production objects outgrowing the shapes the charge was measured on.
type DecodedObjectReading struct {
	Samples uint64
	Last    float64
	Max     float64
}

// decodedSampler keeps the object cache's reading.
type decodedSampler struct {
	stores  atomic.Uint64
	samples atomic.Uint64
	last    atomic.Uint64
	max     atomic.Uint64
}

// observe sizes value against its stored bytes when its turn has come.
func (sampler *decodedSampler) observe(value any, stored int) {
	if stored <= 0 || sampler.stores.Add(1)%decodeSampleEvery != 0 {
		return
	}
	ratio := float64(structuralBytes(value)) / float64(stored)
	sampler.last.Store(math.Float64bits(ratio))
	for {
		current := sampler.max.Load()
		if math.Float64frombits(current) >= ratio || sampler.max.CompareAndSwap(current, math.Float64bits(ratio)) {
			break
		}
	}
	sampler.samples.Add(1)
}

func (sampler *decodedSampler) reading() DecodedObjectReading {
	return DecodedObjectReading{Samples: sampler.samples.Load(),
		Last: math.Float64frombits(sampler.last.Load()), Max: math.Float64frombits(sampler.max.Load())}
}

// structuralBytes is the heap a value's data takes by its structure: the
// value itself, every string's bytes, every slice's backing array at its
// capacity, every map's entries, and every pointer's target, each address
// once - a string, an array or a map two fields share is counted for the
// first. It does not see allocator size classes, a map's buckets beyond its
// entries, or what a decoder leaves unreachable, so it is a lower bound on
// what the value retains. Two views of one array that start at different
// addresses would each be counted; a decoder does not make them. An address
// reached as two kinds - a slice and a pointer to its first element - is
// counted as the first, which undercounts and keeps the bound.
func structuralBytes(value any) uint64 {
	root := reflect.ValueOf(value)
	if !root.IsValid() {
		return 0
	}
	return uint64(root.Type().Size()) + indirectBytes(root, map[uintptr]struct{}{})
}

func indirectBytes(value reflect.Value, seen map[uintptr]struct{}) uint64 {
	first := func(address uintptr) bool {
		if _, counted := seen[address]; counted {
			return false
		}
		seen[address] = struct{}{}
		return true
	}
	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() || !first(value.Pointer()) {
			return 0
		}
		return uint64(value.Type().Elem().Size()) + indirectBytes(value.Elem(), seen)
	case reflect.Interface:
		if value.IsNil() {
			return 0
		}
		held := value.Elem()
		boxed := uint64(0)
		if held.Kind() != reflect.Pointer {
			boxed = uint64(held.Type().Size())
		}
		return boxed + indirectBytes(held, seen)
	case reflect.String:
		if value.Len() == 0 || !first(uintptr(unsafe.Pointer(unsafe.StringData(value.String())))) {
			return 0
		}
		return uint64(value.Len())
	case reflect.Slice:
		if value.Cap() == 0 || !first(value.Pointer()) {
			return 0
		}
		total := uint64(value.Cap()) * uint64(value.Type().Elem().Size())
		for index := 0; index < value.Len(); index++ {
			total += indirectBytes(value.Index(index), seen)
		}
		return total
	case reflect.Array:
		total := uint64(0)
		for index := 0; index < value.Len(); index++ {
			total += indirectBytes(value.Index(index), seen)
		}
		return total
	case reflect.Struct:
		total := uint64(0)
		for index := 0; index < value.NumField(); index++ {
			total += indirectBytes(value.Field(index), seen)
		}
		return total
	case reflect.Map:
		if value.IsNil() || !first(value.Pointer()) {
			return 0
		}
		total := uint64(value.Len()) * uint64(value.Type().Key().Size()+value.Type().Elem().Size())
		entries := value.MapRange()
		for entries.Next() {
			total += indirectBytes(entries.Key(), seen) + indirectBytes(entries.Value(), seen)
		}
		return total
	default:
		return 0
	}
}
