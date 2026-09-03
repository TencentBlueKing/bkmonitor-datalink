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

func TestEvaluatePingUnreachable(t *testing.T) {
	tests := []struct {
		name    string
		value   json.RawMessage
		want    pureDetectionStatus
		wantErr bool
	}{
		{name: "zero", value: json.RawMessage(`0`), want: pureDetectionNormal},
		{name: "one minus epsilon", value: json.RawMessage(`0.9999999999999999`), want: pureDetectionNormal},
		{name: "one", value: json.RawMessage(`1`), want: pureDetectionAnomalous},
		{name: "negative", value: json.RawMessage(`-0.1`), want: pureDetectionUnknown, wantErr: true},
		{name: "greater than one", value: json.RawMessage(`1.1`), want: pureDetectionUnknown, wantErr: true},
		{name: "json null", value: json.RawMessage(`null`), want: pureDetectionUnknown, wantErr: true},
		{name: "non numeric", value: json.RawMessage(`"one"`), want: pureDetectionUnknown, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := evaluatePingUnreachable(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("evaluatePingUnreachable() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("evaluatePingUnreachable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEvaluatePingUnreachableIsStatelessAcrossRecoveryInput(t *testing.T) {
	got, err := evaluatePingUnreachable(json.RawMessage(`1`))
	if err != nil || got != pureDetectionAnomalous {
		t.Fatalf("abnormal input = (%v, %v), want (%v, nil)", got, err, pureDetectionAnomalous)
	}

	got, err = evaluatePingUnreachable(json.RawMessage(`0`))
	if err != nil || got != pureDetectionNormal {
		t.Fatalf("recovery input = (%v, %v), want (%v, nil)", got, err, pureDetectionNormal)
	}
}
