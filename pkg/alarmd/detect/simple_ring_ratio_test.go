// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package detect

import "testing"

func TestEvaluateSimpleRingRatio(t *testing.T) {
	percent := func(value float64) *float64 { return &value }
	previous := func(value float64) *float64 { return &value }

	tests := []struct {
		name string
		in   simpleRingRatioInput
		want pureDetectionStatus
	}{
		{
			name: "floor decline",
			in:   simpleRingRatioInput{current: 79, previous: previous(100), floorPercent: percent(20)},
			want: pureDetectionAnomalous,
		},
		{
			name: "floor equality",
			in:   simpleRingRatioInput{current: 80, previous: previous(100), floorPercent: percent(20)},
			want: pureDetectionAnomalous,
		},
		{
			name: "floor above boundary",
			in:   simpleRingRatioInput{current: 81, previous: previous(100), floorPercent: percent(20)},
			want: pureDetectionNormal,
		},
		{
			name: "ceil rise",
			in:   simpleRingRatioInput{current: 121, previous: previous(100), ceilPercent: percent(20)},
			want: pureDetectionAnomalous,
		},
		{
			name: "ceil equality",
			in:   simpleRingRatioInput{current: 120, previous: previous(100), ceilPercent: percent(20)},
			want: pureDetectionAnomalous,
		},
		{
			name: "ceil below boundary",
			in:   simpleRingRatioInput{current: 119, previous: previous(100), ceilPercent: percent(20)},
			want: pureDetectionNormal,
		},
		{
			name: "dual side floor match",
			in: simpleRingRatioInput{
				current: 79, previous: previous(100), floorPercent: percent(20), ceilPercent: percent(20),
			},
			want: pureDetectionAnomalous,
		},
		{
			name: "dual side ceil match",
			in: simpleRingRatioInput{
				current: 121, previous: previous(100), floorPercent: percent(20), ceilPercent: percent(20),
			},
			want: pureDetectionAnomalous,
		},
		{
			name: "dual side no match",
			in: simpleRingRatioInput{
				current: 100, previous: previous(100), floorPercent: percent(20), ceilPercent: percent(20),
			},
			want: pureDetectionNormal,
		},
		{
			name: "double zero",
			in: simpleRingRatioInput{
				current: 0, previous: previous(0), floorPercent: percent(20), ceilPercent: percent(20),
			},
			want: pureDetectionNormal,
		},
		{
			name: "current zero matches floor",
			in:   simpleRingRatioInput{current: 0, previous: previous(100), floorPercent: percent(20)},
			want: pureDetectionAnomalous,
		},
		{
			name: "previous zero matches ceil",
			in:   simpleRingRatioInput{current: 100, previous: previous(0), ceilPercent: percent(20)},
			want: pureDetectionAnomalous,
		},
		{
			name: "previous missing",
			in:   simpleRingRatioInput{current: 100, floorPercent: percent(20)},
			want: pureDetectionUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := evaluateSimpleRingRatio(tt.in)
			if err != nil {
				t.Fatalf("evaluateSimpleRingRatio() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("evaluateSimpleRingRatio() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEvaluateSimpleRingRatioIsStatelessAcrossRecoveryInput(t *testing.T) {
	percent, previous := 20.0, 100.0

	got, err := evaluateSimpleRingRatio(simpleRingRatioInput{
		current: 50, previous: &previous, floorPercent: &percent,
	})
	if err != nil || got != pureDetectionAnomalous {
		t.Fatalf("abnormal input = (%v, %v), want (%v, nil)", got, err, pureDetectionAnomalous)
	}

	got, err = evaluateSimpleRingRatio(simpleRingRatioInput{
		current: 100, previous: &previous, floorPercent: &percent,
	})
	if err != nil || got != pureDetectionNormal {
		t.Fatalf("recovery input = (%v, %v), want (%v, nil)", got, err, pureDetectionNormal)
	}
}
