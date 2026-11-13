// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"reflect"
	"testing"
	"time"
)

// A Cpus_allowed_list reads as its cores, ranges and single cores alike;
// anything else is no list.
func TestAWindowMeasureCPUListReadsRangesAndCores(t *testing.T) {
	if got, ok := windowMeasureCPUList("0-3,15"); !ok || !reflect.DeepEqual(got, []int{0, 1, 2, 3, 15}) {
		t.Fatalf("0-3,15 read %v %t, want 0 1 2 3 15", got, ok)
	}
	for _, list := range []string{"", "a", "3-1", "0-"} {
		if got, ok := windowMeasureCPUList(list); ok {
			t.Fatalf("%q read %v, want no list", list, got)
		}
	}
}

// The first slice is alone from its first request to the first request of
// another slice, whatever came back between; with no other slice, the span.
func TestTheFirstSliceIsAloneUntilAnotherSliceIsAsked(t *testing.T) {
	at := time.Unix(1_788_000_000, 0)
	requests := []windowMeasureRequest{
		{slice: "b", sent: at.Add(40 * time.Second)},
		{slice: "a", sent: at},
		{slice: "a", sent: at.Add(time.Millisecond)},
		{slice: "c", sent: at.Add(43 * time.Second)},
	}
	if got := windowMeasureFirstSliceAlone(requests, time.Minute); got != 40*time.Second {
		t.Fatalf("first slice alone %s, want 40s", got)
	}
	if got := windowMeasureFirstSliceAlone(requests[1:3], time.Minute); got != time.Minute {
		t.Fatalf("one slice alone %s, want the span", got)
	}
}
