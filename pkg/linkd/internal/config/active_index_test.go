// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import "testing"

func TestActiveIndexDefaultsAndLimits(t *testing.T) {
	c := ActiveIndexConfig{}.WithDefaults()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.PollIntervalSeconds != 1 || c.ReconcileIntervalSeconds != 60 {
		t.Fatal("unexpected defaults")
	}
	for _, bad := range []ActiveIndexConfig{{MaxRows: -1}, {MaxBytes: 65 << 20}, {BatchSize: 101}, {OperationTimeoutSeconds: 61}, {PollIntervalSeconds: 61}} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if err := (ControlPlaneConfig{ActiveIndex: &ActiveIndexConfig{}}).Validate(); err != nil {
		t.Fatal(err)
	}
}
