// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func gatherRotation(t *testing.T, counts *DispatchRotationCounts) map[string]map[string]float64 {
	t.Helper()
	recorder := NewRecorder(BuildInfo{})
	recorder.SetDispatchRotationSource(func() *DispatchRotationCounts { return counts })
	families, err := recorder.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	gathered := map[string]map[string]float64{}
	for _, family := range families {
		for _, series := range family.Metric {
			key := ""
			for _, pair := range series.Label {
				if key != "" {
					key += "/"
				}
				key += pair.GetValue()
			}
			if gathered[family.GetName()] == nil {
				gathered[family.GetName()] = map[string]float64{}
			}
			gathered[family.GetName()][key] = series.GetCounter().GetValue()
		}
	}
	return gathered
}

// Every count the dispatcher's walk keeps is published, and each lands in the
// population it belongs to.
//
// The walk reached the verdict page and nothing else, so the signal that says a
// replica stopped covering its objects could not be alerted on at all. And the
// two populations are deliberately two metrics: rotations and object-turns are
// counted at different rates, and one metric carrying both invites a ratio
// between a numerator and a denominator that do not describe the same thing --
// which is an error a metric name does not announce.
func TestTheWalkPublishesBothItsPopulationsApart(t *testing.T) {
	gathered := gatherRotation(t, &DispatchRotationCounts{
		Completed: 2069, Truncated: 4,
		Offered: 100000, Queued: 90000, DeferredQueueFull: 12, DeferredNotBetter: 500,
	})
	for name, want := range map[string]map[string]float64{
		"bkmonitor_alarmd_dispatch_rotation_total": {"completed": 2069, "truncated": 4},
		"bkmonitor_alarmd_dispatch_walk_total": {
			"offered": 100000, "queued": 90000,
			"deferred_queue_full": 12, "deferred_not_better": 500,
		},
	} {
		published := gathered[name]
		if published == nil {
			t.Fatalf("%s was not published at all", name)
		}
		got := make([]string, 0, len(published))
		for result := range published {
			got = append(got, result)
		}
		sort.Strings(got)
		expected := make([]string, 0, len(want))
		for result := range want {
			expected = append(expected, result)
		}
		sort.Strings(expected)
		if strings.Join(got, ",") != strings.Join(expected, ",") {
			t.Fatalf("%s publishes %v, want exactly %v -- a result nothing writes reads as a "+
				"mechanism that is wired and not firing, and one counted in the wrong population "+
				"invites a ratio that means nothing", name, got, expected)
		}
		for result, value := range want {
			if published[result] != value {
				t.Errorf("%s{result=%s} = %v, want %v", name, result, published[result], value)
			}
		}
	}
}

// Every field of the counts reaches a series.
//
// By reflection rather than by the list above, because that list is a second
// copy: whoever adds a count to the walk and forgets the Collect loop would not
// remember to add it there either. This fails on the field they forgot.
func TestEveryDispatchRotationFieldIsPublished(t *testing.T) {
	counts := DispatchRotationCounts{}
	value := reflect.ValueOf(&counts).Elem()
	for i := 0; i < value.NumField(); i++ {
		// Distinct and non-zero, so a field published from the wrong source
		// fails instead of matching by coincidence.
		value.Field(i).SetUint(uint64(1000 + i))
	}
	gathered := gatherRotation(t, &counts)

	published := map[float64]bool{}
	for _, family := range gathered {
		for _, seen := range family {
			published[seen] = true
		}
	}
	for i := 0; i < value.NumField(); i++ {
		if !published[float64(value.Field(i).Uint())] {
			t.Errorf("DispatchRotationCounts.%s is counted by the walk and reaches no series, so it "+
				"is kept every rotation and nobody outside the page can read it",
				value.Type().Field(i).Name)
		}
	}
}

// A replica that has not walked yet publishes nothing.
//
// Zeros would say the walk ran and turned nobody away, which is the reassuring
// reading of a replica that has not started -- and the reading a rule written
// on "deferred_queue_full is 0" would act on.
func TestAReplicaThatHasNotWalkedPublishesNoRotationCounts(t *testing.T) {
	gathered := gatherRotation(t, nil)
	for _, name := range []string{
		"bkmonitor_alarmd_dispatch_rotation_total", "bkmonitor_alarmd_dispatch_walk_total",
	} {
		if series, ok := gathered[name]; ok {
			t.Errorf("%s published %v before any walk ran; zeros here read as a walk that covered "+
				"everything and turned nobody away", name, series)
		}
	}
}
