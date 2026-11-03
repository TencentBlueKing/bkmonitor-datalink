// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package contract

import (
	"reflect"
	"testing"
)

// The cache is sound only because the top-level verdict does not depend on the
// path. These types are the shapes that could break that: a type that refers
// to itself, and a cycle of three, entered from every member in turn.
type cycleSelf struct {
	Name string     `json:"name"`
	Next *cycleSelf `json:"next"`
}

type cycleA struct {
	B cycleB `json:"b"`
}
type cycleB struct {
	C cycleC `json:"c"`
}
type cycleC struct {
	A *cycleA `json:"a"`
}

func TestClosedTypeVerdictDoesNotDependOnWhereTheWalkStarted(t *testing.T) {
	// Every member of a cycle must answer the same whichever member the walk
	// entered from. If one entry point said closed and another said open, the
	// cached answer would depend on call order, which is the one thing a cache
	// keyed only on the type cannot represent.
	for _, entry := range []reflect.Type{
		reflect.TypeOf(cycleSelf{}), reflect.TypeOf(&cycleSelf{}),
		reflect.TypeOf(cycleA{}), reflect.TypeOf(cycleB{}), reflect.TypeOf(cycleC{}),
	} {
		fresh := canonicalClosedType(entry, nil)
		if fresh {
			t.Fatalf("%s: a type in a cycle must take the strict path", entry)
		}
		if cached := canonicalClosedTypeCached(entry); cached != fresh {
			t.Fatalf("%s: cached %v, uncached %v", entry, cached, fresh)
		}
	}
}

// Every type the pinned branches exercise must get the same answer through the
// cache as without it. This is the whole correctness claim, checked against the
// same denominator the rest of the work uses rather than a fresh list.
func TestCachedClosedTypeAgreesWithTheWalkOnEveryBranchType(t *testing.T) {
	seen := map[reflect.Type]bool{}
	for _, probe := range canonicalBranchProbes() {
		valueType := reflect.TypeOf(probe.value)
		if valueType == nil || seen[valueType] {
			continue
		}
		seen[valueType] = true
		want := canonicalClosedType(valueType, nil)
		if got := canonicalClosedTypeCached(valueType); got != want {
			t.Fatalf("%s (branch %q): cached %v, walk %v", valueType, probe.name, got, want)
		}
		// And again, so a first call that populated the entry and a second
		// call that read it back cannot disagree.
		if got := canonicalClosedTypeCached(valueType); got != want {
			t.Fatalf("%s: second read gave %v, walk %v", valueType, got, want)
		}
	}
	if len(seen) == 0 {
		t.Fatal("no types examined; the probe list stopped carrying typed values")
	}
	t.Logf("distinct probe types checked through the cache: %d", len(seen))
}

func TestCachedClosedTypeHandlesNil(t *testing.T) {
	if canonicalClosedTypeCached(nil) {
		t.Fatal("a nil type must not be reported closed")
	}
}

// The saving this exists for. The walk reads the type graph, so its cost is a
// property of the type rather than of the value, and a deep struct pays it
// every call.
func BenchmarkClosedTypeWalkVersusCache(b *testing.B) {
	valueType := reflect.TypeOf(benchmarkCanonicalRecords(1))
	b.Run("walk", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			canonicalClosedType(valueType, nil)
		}
	})
	b.Run("cached", func(b *testing.B) {
		canonicalClosedTypeCached(valueType)
		b.ResetTimer()
		b.ReportAllocs()
		for range b.N {
			canonicalClosedTypeCached(valueType)
		}
	})
}
