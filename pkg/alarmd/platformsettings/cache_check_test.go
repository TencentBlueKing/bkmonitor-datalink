// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package platformsettings

import (
	"encoding/json"
	"testing"
)

// A field checked alone is refused exactly when a layer carrying it is: the
// reader's verdict and the runtime's cannot disagree about any value.
func TestCheckFieldValueRefusesWhatTheLayerRefuses(t *testing.T) {
	values := []string{"86400", "1", "0", "-3", "1.5", `"86400"`, "null", "true", "false", `["a","b"]`, `[1]`, `[]`, `{}`, `"x"`}
	for _, field := range Fields {
		for _, raw := range values {
			_, layerErr := decodeLayer(map[Field]json.RawMessage{field: json.RawMessage(raw)})
			if checkErr := CheckFieldValue(field, json.RawMessage(raw)); (checkErr == nil) != (layerErr == nil) {
				t.Errorf("%s %s: check %v, layer %v", field, raw, checkErr, layerErr)
			}
		}
	}
	if CheckFieldValue("unknown_field", json.RawMessage("1")) == nil {
		t.Fatal("an unknown field was checked as valid")
	}
}
