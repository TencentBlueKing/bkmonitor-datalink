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

// The saving measured on one hand-written JSON object is a saving on one
// shape. The projection from it to process CPU carries an assumption nobody
// had tested: that the ratio holds across what the callers actually send.
//
// It does not, and the reason is structural rather than incidental. The
// established path takes a different route depending on whether the standard
// encoder produced the bytes:
//
//	closed type      encode, then decode and re-encode          no strict walk
//	anything else    strict walk, then decode and re-encode     walk included
//
// A type is closed only if every field is, and canonicalClosedType says no to
// maps. So CanonicalRecordV2, which carries two map[string]json.RawMessage
// fields, is not closed however ordinary it looks, and the delivery digest
// over a slice of them pays the strict walk on every series. That is the
// heaviest real call site and the single-object benchmark never touched it.
//
// These shapes are built from the callers rather than invented:
//
//	[]DimensionFieldV2   DeriveDimensionIdentityDigestV2, once per series
//	[]CanonicalRecordV2  the provider series delivery digest, once per series
//	PlanSetV2            DerivePlanSetDigestV2, once per publication

func benchmarkCanonicalRecords(records int) []CanonicalRecordV2 {
	out := make([]CanonicalRecordV2, 0, records)
	// The dimension set a host series actually carries, and the encoder's own
	// output for each value, which is what normalizeSeries hands over.
	dimensionNames := []string{"bk_target_ip", "bk_cloud_id", "bk_biz_id", "instance", "job", "device_name"}
	dimensions := make(map[string]json.RawMessage, len(dimensionNames))
	identity := make([]DimensionFieldV2, 0, len(dimensionNames))
	for index, name := range dimensionNames {
		value, err := json.Marshal(fmt.Sprintf("value-%d", index))
		if err != nil {
			panic(err)
		}
		dimensions[name] = value
		identity = append(identity, DimensionFieldV2{Name: name, Value: value})
	}
	for index := range records {
		out = append(out, CanonicalRecordV2{
			RecordID:   fmt.Sprintf("%064x", index),
			SourceTime: int64(1757740800 + index*60),
			BusinessID: "2",
			DimensionIdentity: DimensionIdentityV2{
				Fields: identity, Digest: fmt.Sprintf("%064x", 1),
			},
			Values:       map[string]json.RawMessage{"_result_": json.RawMessage("12345.678")},
			Dimensions:   dimensions,
			ReceivedTime: int64(1757740800 + index*60),
		})
	}
	return out
}

// BenchmarkCanonicalByCallSite runs both forms alternating in one binary over
// each real shape, so the ratio can be read per call site instead of assumed
// uniform.
func BenchmarkCanonicalByCallSite(b *testing.B) {
	shapes := []struct {
		name  string
		value any
	}{
		{"dimension_identity/fields=6", any(benchmarkDimensionFields(6))},
		{"series_delivery/records=1", any(benchmarkCanonicalRecords(1))},
		{"series_delivery/records=12", any(benchmarkCanonicalRecords(12))},
		{"series_delivery/records=60", any(benchmarkCanonicalRecords(60))},
		{"raw_scalar", any(json.RawMessage(`"value-0-abcdefghij"`))},
	}
	for _, shape := range shapes {
		for _, arm := range []struct {
			name string
			mode string
		}{{"established", CanonicalModeEstablished}, {"stream", CanonicalModeStream}} {
			b.Run(shape.name+"/"+arm.name, func(b *testing.B) {
				previous, err := SetCanonicalMode(arm.mode)
				if err != nil {
					b.Fatal(err)
				}
				defer SetCanonicalMode(previous)
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if _, err := CanonicalJSONV2(shape.value); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// Every shape above has to be answered by the single-pass form, or its
// benchmark is measuring the established path twice and reporting the
// difference as noise. This is the same guard as on the branch table, applied
// to the shapes the projection is built from.
func TestCanonicalStreamAnswersEveryRealCallSite(t *testing.T) {
	shapes := map[string]any{
		"dimension_identity": benchmarkDimensionFields(6),
		"series_delivery":    benchmarkCanonicalRecords(12),
		"raw_scalar":         json.RawMessage(`"value-0-abcdefghij"`),
	}
	previous, err := SetCanonicalMode(CanonicalModeStream)
	if err != nil {
		t.Fatal(err)
	}
	defer SetCanonicalMode(previous)
	for name, value := range shapes {
		t.Run(name, func(t *testing.T) {
			beforeServed, beforeDeclined := CanonicalStreamCounts()
			if _, err := CanonicalJSONV2(value); err != nil {
				t.Fatal(err)
			}
			served, declined := CanonicalStreamCounts()
			if declined != beforeDeclined {
				t.Fatal("declined into the established path; this shape gets no saving at all")
			}
			if served == beforeServed {
				t.Fatal("neither served nor declined")
			}
		})
	}
}

// And every shape has to produce identical bytes, which the benchmark alone
// would not notice: a faster wrong answer still benchmarks faster.
func TestCanonicalStreamAgreesOnRealCallSites(t *testing.T) {
	for _, records := range []int{1, 12, 60} {
		t.Run(fmt.Sprintf("series_delivery/records=%d", records), func(t *testing.T) {
			canonicalStreamAgrees(t, benchmarkCanonicalRecords(records))
		})
	}
	for _, fields := range []int{1, 6, 16} {
		t.Run(fmt.Sprintf("dimension_identity/fields=%d", fields), func(t *testing.T) {
			canonicalStreamAgrees(t, benchmarkDimensionFields(fields))
		})
	}
}
