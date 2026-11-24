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

func TestSeverityUpgradePolicy(t *testing.T) {
	if (LifecycleConfig{}).WithDefaults().SeverityUpgradePolicy != "close_and_create" {
		t.Fatal("unexpected default")
	}
	for _, policy := range []string{"update_current", "close_and_create"} {
		if err := (LifecycleConfig{SeverityUpgradePolicy: policy}).Validate(); err != nil {
			t.Fatalf("%s: %v", policy, err)
		}
	}
	if (LifecycleConfig{SeverityUpgradePolicy: "invalid"}).Validate() == nil {
		t.Fatal("invalid policy accepted")
	}
}
