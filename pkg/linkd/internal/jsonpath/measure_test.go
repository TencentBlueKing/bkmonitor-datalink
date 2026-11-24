// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package jsonpath

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestMeasureMatchesJSONEncoding(t *testing.T) {
	for _, value := range []any{nil, false, true, "plain", "\"\\\n\r\t\b\f\x00<>&\u2028\u2029中文", string([]byte{0xff, 0xff}), json.Number("9007199254740993"), json.Number("1e999"), 1e-7, -1e-9, 1e-6, 1e20, 1e21, math.Copysign(0, -1), map[string]any(nil), []any(nil), map[string]any{"x": []any{json.Number("1.0"), nil, true}, "<": "&"}} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		measured, err := Measure(value)
		if err != nil || measured.Bytes != len(encoded) {
			t.Fatalf("value=%#v measured=%+v encoded=%q err=%v", value, measured, encoded, err)
		}
	}
	for _, value := range []any{json.Number("true"), json.Number("01"), json.Number(""), json.Number("1\n"), math.NaN(), math.Inf(1), strings.Repeat("<", 1<<18)} {
		if _, err := Measure(value); err == nil {
			t.Fatalf("accepted invalid/oversized value %T", value)
		}
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	if _, err := Measure(cycle); err == nil {
		t.Fatal("accepted cyclic tree")
	}
}

type customNativeObject map[string]any

func (customNativeObject) MarshalJSON() ([]byte, error) { return []byte(`{"custom":true}`), nil }

func TestNativeMatchesEncodingSemanticsAndOwnsValues(t *testing.T) {
	for _, value := range []any{[]byte("bytes"), json.RawMessage(nil), json.RawMessage(`{"n":1e999,"s":"\u0061"}`), customNativeObject{"ignored": true}, map[string]any{string([]byte{0xff}): "last", string([]byte{0xfe}): "first"}} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var expected any
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		if err := decoder.Decode(&expected); err != nil {
			t.Fatal(err)
		}
		want, _ := json.Marshal(expected)
		got, err := Native(value)
		if err != nil {
			t.Fatal(err)
		}
		actual, _ := json.Marshal(got)
		if !bytes.Equal(actual, want) {
			t.Fatalf("native=%s encoded=%s", actual, want)
		}
		measured, err := Measure(value)
		if err != nil || measured.Bytes != len(encoded) {
			t.Fatalf("measure=%+v encoded=%s err=%v", measured, encoded, err)
		}
	}
	input := map[string]any{"items": []any{"original"}}
	got, err := Native(input)
	if err != nil {
		t.Fatal(err)
	}
	input["items"].([]any)[0] = "changed"
	if got.(map[string]any)["items"].([]any)[0] != "original" {
		t.Fatal("native value shares caller's array")
	}
}

func TestSetWithUndoRestoresMissingParentsAndArray(t *testing.T) {
	target, err := ParseTarget("$.extra_data.created.nested.value")
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"extra_data": map[string]any{"items": []any{nil, "old"}}}
	undo, err := target.SetWithUndo(input, "new")
	if err != nil {
		t.Fatal(err)
	}
	undo()
	extra := input["extra_data"].(map[string]any)
	if _, exists := extra["created"]; exists {
		t.Fatal("new parent survived rollback")
	}
	array, _ := ParseTarget("$.extra_data.items[0]")
	undo, err = array.SetWithUndo(input, false)
	if err != nil {
		t.Fatal(err)
	}
	undo()
	if extra["items"].([]any)[0] != nil {
		t.Fatal("null changed during rollback")
	}
	bad, _ := ParseTarget("$.extra_data.created.items[0]")
	if _, err := bad.SetWithUndo(input, true); err == nil {
		t.Fatal("created a missing array")
	}
	if _, exists := extra["created"]; exists {
		t.Fatal("failed setter left a parent")
	}
}
