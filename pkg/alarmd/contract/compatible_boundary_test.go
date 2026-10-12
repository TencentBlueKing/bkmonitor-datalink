// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"testing"
	"time"
)

// A Plan detected every minute over four-minute windows has a record on the
// aggregation grid once in four; the others are between boundaries, and the
// compatible protocol has no message for them of any kind. A Plan detected
// once an aggregation interval has no record between boundaries, and every
// other protocol carries every record.
func TestOnlyTheDetectionsOnTheAggregationGridHaveACompatibleMessage(t *testing.T) {
	stepped := ExecutionSemanticsV2{AggregationInterval: 240, EvaluationInterval: 60}
	once := ExecutionSemanticsV2{AggregationInterval: 240, EvaluationInterval: 240}
	for _, test := range []struct {
		semantics ExecutionSemanticsV2
		source    int64
		off       bool
	}{
		{stepped, 240, false}, {stepped, 300, true}, {stepped, 420, true}, {stepped, 480, false},
		{once, 300, false}, {ExecutionSemanticsV2{}, 300, false},
	} {
		if got := CompatibleOffBoundary(test.semantics, nil, test.source); got != test.off {
			t.Fatalf("%+v at %d: off boundary %t, want %t", test.semantics, test.source, got, test.off)
		}
	}
	if EventHasMessageAt(WireFormatPythonCompatible, TriggerEventAbnormal, true) ||
		!EventHasMessageAt(WireFormatPythonCompatible, TriggerEventAbnormal, false) ||
		!EventHasMessageAt(WireFormatStandardRawEvent, TriggerEventAbnormal, true) ||
		!EventHasMessageAt(WireFormatStandardRawEvent, TriggerEventRecovery, true) ||
		EventHasMessageAt(WireFormatPythonCompatible, TriggerEventRecovery, false) {
		t.Fatal("the compatible protocol carries an anomaly on a boundary and nothing else; every other protocol carries everything")
	}
	if !NoMessageForAt(WireFormatPythonCompatible, TriggerEventAbnormal, true, true) || NoMessageForAt(WireFormatPythonCompatible, TriggerEventAbnormal, false, true) {
		t.Fatal("an anomaly between boundaries is left without a message only where the compatibility context is carried")
	}
}

// The aggregation grid is laid in the query's time zone, as the query
// service lays an aligned query's buckets: a day detected every minute under
// UTC+8 has its boundary at the local midnight, 16:00 UTC, and the UTC
// midnight is one of the detections between two boundaries.
func TestADaysBoundaryIsTheLocalMidnightOfTheQuerysTimeZone(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	day := ExecutionSemanticsV2{AggregationInterval: 86400, EvaluationInterval: 60}
	const utcMidnight, localMidnight = 1_700_092_800, 1_700_150_400
	for _, test := range []struct {
		location *time.Location
		source   int64
		off      bool
	}{
		{shanghai, localMidnight, false}, {shanghai, utcMidnight, true}, {shanghai, localMidnight + 60, true},
		{nil, utcMidnight, false}, {nil, localMidnight, true},
	} {
		if got := CompatibleOffBoundary(day, test.location, test.source); got != test.off {
			t.Fatalf("%v at %d: off boundary %t, want %t", test.location, test.source, got, test.off)
		}
	}
}
