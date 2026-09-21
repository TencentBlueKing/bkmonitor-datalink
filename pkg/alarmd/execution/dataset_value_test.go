// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestRecordViewValueIsReadOnly(t *testing.T) {
	dataset := NewDataset([]contract.CanonicalRecordV2{{Values: map[string]json.RawMessage{"value": json.RawMessage(`12`)}}})
	record, _ := dataset.Record(0)
	value, ok := record.Value("value")
	if !ok || string(value) != "12" {
		t.Fatalf("Value() = %q, %v", value, ok)
	}
	value[0] = '9'
	again, _ := record.Value("value")
	if string(again) != "12" {
		t.Fatalf("Value() leaked writable bytes: %q", again)
	}
	if _, ok := record.Value("missing"); ok {
		t.Fatal("Value() found missing field")
	}
}
