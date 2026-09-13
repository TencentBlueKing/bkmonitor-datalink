// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package detect

import (
	"encoding/json"
	"testing"
)

func TestEvaluateProcPort(t *testing.T) {
	tests := []struct {
		name    string
		in      procPortInput
		want    pureDetectionStatus
		wantErr bool
	}{
		{
			name: "process missing",
			in:   procPortInput{procExists: json.RawMessage(`0`)},
			want: pureDetectionAnomalous,
		},
		{
			name: "non listening port",
			in: procPortInput{
				procExists: json.RawMessage(`1`),
				dimensions: map[string]json.RawMessage{"nonlisten": json.RawMessage(`"80"`)},
			},
			want: pureDetectionAnomalous,
		},
		{
			name: "inaccurate listening address",
			in: procPortInput{
				procExists: json.RawMessage(`1`),
				dimensions: map[string]json.RawMessage{"not_accurate_listen": json.RawMessage(`"127.0.0.1:80"`)},
			},
			want: pureDetectionAnomalous,
		},
		{
			name: "empty list tokens",
			in: procPortInput{
				procExists: json.RawMessage(`1`),
				dimensions: map[string]json.RawMessage{
					"nonlisten":           json.RawMessage(`"[]"`),
					"not_accurate_listen": json.RawMessage(`"[]"`),
				},
			},
			want: pureDetectionNormal,
		},
		{
			name: "null tokens",
			in: procPortInput{
				procExists: json.RawMessage(`1`),
				dimensions: map[string]json.RawMessage{
					"nonlisten":           json.RawMessage(`"null"`),
					"not_accurate_listen": json.RawMessage(`"null"`),
				},
			},
			want: pureDetectionNormal,
		},
		{
			name: "dimension fields missing",
			in:   procPortInput{procExists: json.RawMessage(`1`)},
			want: pureDetectionNormal,
		},
		{
			name:    "process value invalid",
			in:      procPortInput{procExists: json.RawMessage(`"alive"`)},
			want:    pureDetectionUnknown,
			wantErr: true,
		},
		{
			name: "dimension value invalid",
			in: procPortInput{
				procExists: json.RawMessage(`1`),
				dimensions: map[string]json.RawMessage{"nonlisten": json.RawMessage(`[]`)},
			},
			want:    pureDetectionUnknown,
			wantErr: true,
		},
		{
			name: "dimension json null invalid",
			in: procPortInput{
				procExists: json.RawMessage(`1`),
				dimensions: map[string]json.RawMessage{"nonlisten": json.RawMessage(`null`)},
			},
			want:    pureDetectionUnknown,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := evaluateProcPort(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("evaluateProcPort() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("evaluateProcPort() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEvaluateProcPortIgnoresIdentityDimensions(t *testing.T) {
	base := procPortInput{
		procExists: json.RawMessage(`1`),
		dimensions: map[string]json.RawMessage{
			"nonlisten":           json.RawMessage(`"[]"`),
			"not_accurate_listen": json.RawMessage(`"[]"`),
		},
	}
	withDifferentIdentity := procPortInput{
		procExists: json.RawMessage(`1`),
		dimensions: map[string]json.RawMessage{
			"nonlisten":             json.RawMessage(`"[]"`),
			"not_accurate_listen":   json.RawMessage(`"[]"`),
			"bk_target_ip":          json.RawMessage(`"192.0.2.10"`),
			"bk_target_cloud_id":    json.RawMessage(`"7"`),
			"display_name":          json.RawMessage(`"example"`),
			"unrelated_dynamic_key": json.RawMessage(`"ignored"`),
		},
	}

	want, err := evaluateProcPort(base)
	if err != nil {
		t.Fatalf("evaluateProcPort(base) error = %v", err)
	}
	got, err := evaluateProcPort(withDifferentIdentity)
	if err != nil {
		t.Fatalf("evaluateProcPort(with identity) error = %v", err)
	}
	if got != want {
		t.Fatalf("identity dimensions changed result from %v to %v", want, got)
	}
}

func TestEvaluateProcPortIsStatelessAcrossRecoveryInput(t *testing.T) {
	got, err := evaluateProcPort(procPortInput{procExists: json.RawMessage(`0`)})
	if err != nil || got != pureDetectionAnomalous {
		t.Fatalf("abnormal input = (%v, %v), want (%v, nil)", got, err, pureDetectionAnomalous)
	}

	got, err = evaluateProcPort(procPortInput{procExists: json.RawMessage(`1`)})
	if err != nil || got != pureDetectionNormal {
		t.Fatalf("recovery input = (%v, %v), want (%v, nil)", got, err, pureDetectionNormal)
	}
}
