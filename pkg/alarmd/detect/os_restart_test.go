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

func TestEvaluateOSRestart(t *testing.T) {
	previous := func(value float64) *float64 { return &value }

	tests := []struct {
		name string
		in   osRestartInput
		want pureDetectionStatus
	}{
		{
			name: "zero uptime",
			in:   osRestartInput{current: 0, previous: previous(100), hasTenMinutePoint: true},
			want: pureDetectionNormal,
		},
		{
			name: "six hundred boundary",
			in:   osRestartInput{current: 600, previous: previous(700), hasTenMinutePoint: true},
			want: pureDetectionAnomalous,
		},
		{
			name: "six hundred and one",
			in:   osRestartInput{current: 601, previous: previous(700), hasTenMinutePoint: true},
			want: pureDetectionNormal,
		},
		{
			name: "previous missing",
			in:   osRestartInput{current: 300, hasTenMinutePoint: true},
			want: pureDetectionAnomalous,
		},
		{
			name: "uptime declined",
			in:   osRestartInput{current: 300, previous: previous(700), hasTenMinutePoint: true},
			want: pureDetectionAnomalous,
		},
		{
			name: "uptime unchanged",
			in:   osRestartInput{current: 300, previous: previous(300), hasTenMinutePoint: true},
			want: pureDetectionNormal,
		},
		{
			name: "uptime increased",
			in:   osRestartInput{current: 300, previous: previous(200), hasTenMinutePoint: true},
			want: pureDetectionNormal,
		},
		{
			name: "ten minute point only",
			in:   osRestartInput{current: 300, previous: previous(700), hasTenMinutePoint: true},
			want: pureDetectionAnomalous,
		},
		{
			name: "twenty five minute point only",
			in:   osRestartInput{current: 300, previous: previous(700), hasTwentyFiveMinutePoint: true},
			want: pureDetectionAnomalous,
		},
		{
			name: "both older points missing",
			in:   osRestartInput{current: 300, previous: previous(700)},
			want: pureDetectionNormal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := evaluateOSRestart(tt.in)
			if err != nil {
				t.Fatalf("evaluateOSRestart() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("evaluateOSRestart() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEvaluateOSRestartIsStatelessAcrossRecoveryInput(t *testing.T) {
	previous := 700.0

	got, err := evaluateOSRestart(osRestartInput{
		current: 300, previous: &previous, hasTenMinutePoint: true,
	})
	if err != nil || got != pureDetectionAnomalous {
		t.Fatalf("abnormal input = (%v, %v), want (%v, nil)", got, err, pureDetectionAnomalous)
	}

	got, err = evaluateOSRestart(osRestartInput{
		current: 601, previous: &previous, hasTenMinutePoint: true,
	})
	if err != nil || got != pureDetectionNormal {
		t.Fatalf("recovery input = (%v, %v), want (%v, nil)", got, err, pureDetectionNormal)
	}
}
