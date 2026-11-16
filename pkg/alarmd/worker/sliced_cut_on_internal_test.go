// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/inputslice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The dimension the next read is cut on, by what this read did and what
// the read before chose, in order: this read's root split, its sample read
// whole, the one before while it is still a dimension, and only then, for a
// read whole that ran out of time with nothing sampled, the first
// dimension. A read whole the provider timed out keeps the choice before
// it: a small Query Group's one slow answer is no reason to forget the
// dimension its sample chose.
func TestTheDimensionChosenBeforeOutlivesAReadWholeThatTimedOut(t *testing.T) {
	dimensions := []string{"cloud", "host"}
	before := slicedReading{dimensions: dimensions, cut: inputslice.FixedCut("host"), cutFrom: observability.InputSliceCutOnWholeSample}
	for _, test := range []struct {
		name             string
		before           slicedReading
		timedOut, failed bool
		wantOn, wantFrom string
	}{
		{"chosen before, timed out", before, true, false, "host", observability.InputSliceCutOnWholeSample},
		{"chosen before, failed", before, false, true, "host", observability.InputSliceCutOnWholeSample},
		{"chosen before, gone", slicedReading{dimensions: dimensions, cut: inputslice.FixedCut("pod")}, true, false, "cloud", observability.InputSliceCutOnFirstDimension},
		{"none before, timed out", slicedReading{}, true, false, "cloud", observability.InputSliceCutOnFirstDimension},
		{"none before, answered", slicedReading{}, false, false, "", ""},
	} {
		reading := &sliceReading{wholeTimedOut: test.timedOut}
		cut, from := reading.cutOnNext(dimensions, test.before, test.failed)
		on := cut.Dimension
		if on != test.wantOn || from != test.wantFrom {
			t.Errorf("%s: cut on %q from %q, want %q from %q", test.name, on, from, test.wantOn, test.wantFrom)
		}
	}
}
