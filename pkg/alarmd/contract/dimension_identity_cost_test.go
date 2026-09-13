// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package contract

import (
	"encoding/json"
	"fmt"
	"testing"
)

// A production CPU profile put 57% of this process in CanonicalJSONV2, and
// 92% of the share that runs while a query permit is held comes from one
// caller: uq.normalizeSeries, once per series. This file measures where that
// call actually spends, so the optimisation is chosen against a number rather
// than against a reading of the code.
//
// The shape of the input is taken from the caller rather than invented:
// normalizeSeries marshals each group value with the standard encoder and
// hands the result over as json.RawMessage, so every value here is produced
// the same way.
func benchmarkDimensionFields(fields int) []DimensionFieldV2 {
	out := make([]DimensionFieldV2, 0, fields)
	for index := range fields {
		// Sorted and unique, which is what the deriver requires, and what
		// normalizeSeries produces with its insertion sort.
		name := fmt.Sprintf("dim_%02d", index)
		value, err := json.Marshal(fmt.Sprintf("value-%d-%s", index, "abcdefghij"))
		if err != nil {
			panic(err)
		}
		out = append(out, DimensionFieldV2{Name: name, Value: value})
	}
	return out
}

func BenchmarkDeriveDimensionIdentityDigestV2(b *testing.B) {
	for _, fields := range []int{1, 4, 8, 16} {
		b.Run(fmt.Sprintf("fields=%d", fields), func(b *testing.B) {
			input := benchmarkDimensionFields(fields)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := DeriveDimensionIdentityDigestV2("tenant", "2", input); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// The deriver calls CanonicalJSONV2 twice over the same bytes: once per field
// inside the validation loop, whose result is read for one byte and then
// dropped, and once over the whole slice, which canonicalises every value
// again. These two benchmarks separate those halves so the split is measured
// rather than assumed.
func BenchmarkDimensionIdentityPerFieldLoop(b *testing.B) {
	for _, fields := range []int{1, 4, 8, 16} {
		b.Run(fmt.Sprintf("fields=%d", fields), func(b *testing.B) {
			input := benchmarkDimensionFields(fields)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				for _, dimension := range input {
					canonical, err := CanonicalJSONV2(dimension.Value)
					if err != nil || len(canonical) == 0 {
						b.Fatal("unexpected", err)
					}
				}
			}
		})
	}
}

func BenchmarkDimensionIdentityWholeSlice(b *testing.B) {
	for _, fields := range []int{1, 4, 8, 16} {
		b.Run(fmt.Sprintf("fields=%d", fields), func(b *testing.B) {
			input := benchmarkDimensionFields(fields)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := CanonicalJSONV2(input); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// CanonicalJSONV2 over one already-encoded scalar is the unit both halves are
// built from. It is measured on its own because the fix under consideration
// changes only this call, and a saving here has to be big enough to survive
// being one part of the two above.
func BenchmarkCanonicalJSONV2Scalar(b *testing.B) {
	value := json.RawMessage(`"value-0-abcdefghij"`)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := CanonicalJSONV2(value); err != nil {
			b.Fatal(err)
		}
	}
}
