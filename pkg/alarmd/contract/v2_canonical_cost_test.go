// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

type canonicalPointerMarshaler struct{ Value int }

func (*canonicalPointerMarshaler) MarshalJSON() ([]byte, error) { return []byte(`{"x":1,"x":2}`), nil }

type canonicalTextMarshaler string

func (canonicalTextMarshaler) MarshalText() ([]byte, error) { return []byte("x"), nil }

func TestCanonicalClosedTypeBoundary(t *testing.T) {
	for _, v := range []any{struct {
		Name   string
		Levels []struct {
			ID    uint32
			Value float64
		}
	}{}, [2]int{}, (*int)(nil), true} {
		if !canonicalClosedType(reflect.TypeOf(v), nil) {
			t.Fatalf("closed type rejected: %T", v)
		}
	}
	for _, v := range []any{[]byte(`{}`), json.RawMessage(`{}`), json.Number("1"), map[string]int{}, struct{ Any any }{}, canonicalPointerMarshaler{}, []canonicalPointerMarshaler{}, struct{ Text canonicalTextMarshaler }{}} {
		if canonicalClosedType(reflect.TypeOf(v), nil) {
			t.Fatalf("unsafe type accepted: %T", v)
		}
	}
	type cycle struct{ Next *cycle }
	if canonicalClosedType(reflect.TypeOf(cycle{}), nil) {
		t.Fatal("recursive type requires fallback")
	}
}

func TestCanonicalClosedTypedMatchesStrictRaw(t *testing.T) {
	value := struct {
		Z      []int   `json:"z"`
		A      string  `json:"a"`
		Number float64 `json:"number"`
	}{[]int{2, 1}, "<世界>\u2028\u2029", 1.25}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	want, err := CanonicalJSONV2(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	got, err := CanonicalJSONV2(value)
	if err != nil || string(got) != string(want) {
		t.Fatalf("typed=%s strict=%s err=%v", got, want, err)
	}
	for _, v := range []any{[]byte(`{"x":1,"x":2}`), struct{ Raw json.RawMessage }{json.RawMessage(`{"x":1,"x":2}`)}, struct{ Any any }{json.RawMessage(`{"x":1,"x":2}`)}, &canonicalPointerMarshaler{}} {
		if _, err := CanonicalJSONV2(v); err == nil {
			t.Fatalf("duplicate accepted through %T", v)
		}
	}
}

func TestCanonicalRestoreNoEscapeReusesOwnedEncoding(t *testing.T) {
	input := []byte(`{"value":"plain"}`)
	got := restoreJSONLineSeparatorsV2(input)
	if &got[0] != &input[0] {
		t.Fatal("unescaped owned encoding copied")
	}
	if cap(got) != len(got) {
		t.Fatal("output exposes spare encoder buffer capacity")
	}
}

func TestCanonicalClosedTypedRetainsNumberAndUnicodeRejection(t *testing.T) {
	for _, value := range []any{struct{ Value float64 }{math.NaN()}, struct{ Value float64 }{math.Inf(1)}, json.Number("01"), json.RawMessage(`{"x":"\ud800"}`), json.RawMessage("{} {}")} {
		if _, err := CanonicalJSONV2(value); err == nil {
			t.Fatalf("invalid value accepted: %T", value)
		}
	}
	for _, value := range []any{struct{ Value string }{"\\u2028"}, struct{ Value float64 }{math.Copysign(0, -1)}, struct{ Value uint64 }{math.MaxUint64}} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		got, err := CanonicalJSONV2(value)
		if err != nil {
			t.Fatal(err)
		}
		want, err := CanonicalJSONV2(json.RawMessage(raw))
		if err != nil || string(got) != string(want) {
			t.Fatalf("typed bytes differ: %s %s %v", got, want, err)
		}
	}
}
