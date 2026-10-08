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
	"encoding/json"
	"strings"
	"testing"
	"unsafe"
)

// The structure of a value is what its strings, slices, maps and pointers
// hold beside it, each address once: a string, an array or a map that two
// fields share is one allocation, and counted as one.
func TestAValuesStructureIsCountedOnce(t *testing.T) {
	type leaf struct{ Name string }
	shared := &leaf{Name: strings.Repeat("x", 100)}
	name, items, tags := strings.Repeat("n", 3), make([]int64, 2, 4), map[string]string{"k": "vv"}
	value := struct {
		Name, Alias  string
		Items, Again []int64
		Refs         []*leaf
		Tags, Same   map[string]string
	}{Name: name, Alias: name, Items: items, Again: items, Refs: []*leaf{shared, shared}, Tags: tags, Same: tags}
	want := uint64(unsafe.Sizeof(value)) + 3 + 4*8 + // the name and the items' backing array at its capacity
		2*uint64(unsafe.Sizeof(shared)) + uint64(unsafe.Sizeof(leaf{})) + 100 + // two references, one leaf
		uint64(unsafe.Sizeof("")*2) + 1 + 2 // one map entry and its strings
	if got := structuralBytes(value); got != want {
		t.Fatalf("structural bytes = %d, want %d", got, want)
	}
}

// The object cache sizes one object in decodeSampleEvery it stores and keeps
// the last and largest ratio. No bound is asserted on the ratio: a Query
// Group of nearly empty Plans structures at more than its compact payload,
// and that is the kind of object the reading exists to find in production.
func TestTheObjectCacheSamplesWhatItsObjectsTakeDecoded(t *testing.T) {
	object := QueryGroupObject{Identity: "qg-sampled", ScheduleRevision: "schedule"}
	for index := 0; index < 9; index++ {
		object.Plans = append(object.Plans, QueryGroupPlanObject{ScheduleRevision: "schedule"})
	}
	payload, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	cache := newObjectReadCache(1024, 1<<30)
	for index := 0; index < 2*decodeSampleEvery-1; index++ {
		var decoded QueryGroupObject
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatal(err)
		}
		cache.store(nil, strings.Repeat("k", index+1), storedQueryGroupObject{object: decoded}, len(payload))
	}
	reading := cache.decoded.reading()
	if reading.Samples != 1 || reading.Last <= 0 || reading.Max != reading.Last {
		t.Fatalf("reading after %d stores = %+v, want one sample", 2*decodeSampleEvery-1, reading)
	}
	var decoded QueryGroupObject
	_ = json.Unmarshal(payload, &decoded)
	cache.store(nil, "last", storedQueryGroupObject{object: decoded}, len(payload))
	if reading := cache.decoded.reading(); reading.Samples != 2 {
		t.Fatalf("reading after %d stores = %+v, want two samples", 2*decodeSampleEvery, reading)
	}
	if reading := (&RedisCatalogRepository{}).DecodedObjectReading(); reading != (DecodedObjectReading{}) {
		t.Fatalf("a repository without a cache reads %+v, want zero", reading)
	}
}
