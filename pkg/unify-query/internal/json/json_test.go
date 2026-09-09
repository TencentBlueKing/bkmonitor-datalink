// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package json

import (
	stdjson "encoding/json"
	"strings"
	"testing"
)

func TestDecoderUseNumberPreservesLargeInteger(t *testing.T) {
	decoder := NewDecoder(strings.NewReader(`{"cursor":9007199254740993}`))
	decoder.UseNumber()

	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}

	cursor, ok := value["cursor"].(stdjson.Number)
	if !ok {
		t.Fatalf("cursor has type %T, want json.Number", value["cursor"])
	}
	if cursor.String() != "9007199254740993" {
		t.Fatalf("cursor = %s, want 9007199254740993", cursor)
	}
}
