// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package core

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func directRecord() map[string]any {
	return map[string]any{"dataid": 1002, "__scope_type": "APM", "__scope": "checkout%2Dweb-http%2Dclient", "__metric": "request_duration_sum", "__name": "request.duration", "__original_type": "histogram", "__type": "counter", "__unit": "s", "__description": "请求耗时", "__temporality": "cumulative", "__interval": nil, "__usage": "sum"}
}

func decodeOne(t *testing.T, raw map[string]any) Record {
	t.Helper()
	b, err := json.Marshal([]any{raw})
	if err != nil {
		t.Fatal(err)
	}
	records, dropped, err := DecodeMessage(b)
	if err != nil || len(records) != 1 || len(dropped) != 0 {
		t.Fatalf("decode: records=%v dropped=%v err=%v", records, dropped, err)
	}
	return records[0]
}

func TestDirectAndCollectorHaveSameFingerprint(t *testing.T) {
	raw := directRecord()
	expected := decodeOne(t, raw)
	labels := map[string]any{"present": "11", "dataid": "1002"}
	for k, v := range raw {
		if k == "dataid" {
			continue
		}
		if v == nil {
			v = ""
		}
		labels[strings.TrimPrefix(k, "__")] = v
	}
	b, err := json.Marshal(map[string]any{"data": []any{map[string]any{"dimension": labels}}})
	if err != nil {
		t.Fatal(err)
	}
	records, dropped, err := DecodeMessage(b)
	if err != nil || len(records) != 1 || len(dropped) != 0 {
		t.Fatalf("records=%v dropped=%v err=%v", records, dropped, err)
	}
	actual := records[0]
	if actual.Fingerprint != expected.Fingerprint || actual.Meta != expected.Meta || actual.ScopeHash != expected.ScopeHash {
		t.Fatalf("protocols disagree: %+v / %+v", actual, expected)
	}
	if actual.Labels != `{"scope_name":"http-client","service_name":"checkout-web"}` {
		t.Fatal(actual.Labels)
	}
}

func TestCollectorPresenceAndRejectsMalformedAPMScope(t *testing.T) {
	b := []byte(`{"data":[{"dimension":{"dataid":"1002","bk_biz_id":"2","scope_type":"APM","scope":"checkout-http","metric":"duration_sum","name":"duration","original_type":"histogram","type":"counter","unit":"","description":"","interval":"","usage":"sum","temporality":"cumulative","present":"8"}}]}`)
	records, dropped, err := DecodeMessage(b)
	if err != nil || len(records) != 1 || len(dropped) != 0 {
		t.Fatalf("%v %v %v", records, dropped, err)
	}
	if records[0].Meta != `{"temporality":"cumulative","usage":"sum"}` {
		t.Fatal(records[0].Meta)
	}

	b = []byte(`{"data":[{"dimension":{"dataid":"1002","bk_biz_id":"2","scope_type":"APM","scope":"ot-kapm-test-io.opentelemetry.sdk.trace","metric":"duration_sum","name":"duration","original_type":"histogram","type":"counter","present":"0"}}]}`)
	records, dropped, err = DecodeMessage(b)
	if err != nil || len(records) != 0 || dropped["invalid_record"] != 1 {
		t.Fatalf("malformed scope accepted: records=%v dropped=%v err=%v", records, dropped, err)
	}

	b = []byte(`[{"dataid":1002,"__scope_type":"DEFAULT","__scope":"","__metric":"queue_size","__name":"queue_size","__original_type":"gauge","__type":"gauge"}]`)
	records, dropped, err = DecodeMessage(b)
	if err != nil || len(records) != 1 || len(dropped) != 0 || records[0].Labels != `{}` {
		t.Fatalf("DEFAULT scope should have empty labels: records=%v dropped=%v err=%v", records, dropped, err)
	}
}

func TestScopeDecoderMatchesPlan(t *testing.T) {
	labels, hash, err := decodeScope("APM", "checkout%2Dweb-http%2Dclient")
	if err != nil || labels != `{"scope_name":"http-client","service_name":"checkout-web"}` || hash != digest("APMscope_name:http-client||service_name:checkout-web") {
		t.Fatalf("unexpected APM scope decode: labels=%s hash=%s err=%v", labels, hash, err)
	}
	if _, _, err := decodeScope("APM", "checkout-http-client"); err == nil {
		t.Fatal("APM scope with multiple unescaped separators was accepted")
	}
	if _, _, err := decodeScope("APM", "checkout%2dweb-http"); err == nil {
		t.Fatal("non-canonical lowercase escape was accepted")
	}
	if labels, _, err := decodeScope("DEFAULT", ""); err != nil || labels != `{}` {
		t.Fatalf("empty DEFAULT scope should decode to no labels: labels=%s err=%v", labels, err)
	}
	if _, _, err := decodeScope("CUSTOM", ""); err == nil {
		t.Fatal("empty non-DEFAULT scope was accepted")
	}
}

func TestFingerprintNormalizationAndIdentity(t *testing.T) {
	raw := directRecord()
	raw["__description"] = "<耗时>😀"
	a := decodeOne(t, raw)
	raw["__description"] = "<耗时>�"
	raw["__metric"] = "different_name"
	b := decodeOne(t, raw)
	if a.Fingerprint != b.Fingerprint {
		t.Fatal("field name or four-byte Unicode changed canonical fingerprint")
	}
	if strings.Contains(a.Meta, `\u003c`) || !strings.Contains(a.Meta, "�") {
		t.Fatal(a.Meta)
	}
	raw["__unit"] = "ms"
	c := decodeOne(t, raw)
	if c.Fingerprint == a.Fingerprint {
		t.Fatal("unit change did not change fingerprint")
	}
	raw["__description"] = nil
	d := decodeOne(t, raw)
	delete(raw, "__description")
	e := decodeOne(t, raw)
	if d.Fingerprint != e.Fingerprint {
		t.Fatal("null and absent fields differ")
	}
}

func TestInvalidRecordDoesNotDropValidSiblings(t *testing.T) {
	valid := directRecord()
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{"invalid interval", "__interval", -1},
		{"scope delimiter", "__scope", "a||b-http"},
		{"extra APM separator", "__scope", "service-http-client"}, {"missing APM separator", "__scope", "service"},
		{"empty APM service", "__scope", "-http"}, {"empty APM scope", "__scope", "service-"},
		{"empty name", "__name", ""}, {"long metric", "__metric", strings.Repeat("a", 256)},
		{"large DataID", "dataid", int64(math.MaxInt32) + 1}, {"null type", "__type", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := directRecord()
			bad[tc.field] = tc.value
			b, _ := json.Marshal([]any{bad, valid})
			r, d, err := DecodeMessage(b)
			if err != nil || len(r) != 1 || d["invalid_record"] != 1 {
				t.Fatalf("records=%v dropped=%v err=%v", r, d, err)
			}
		})
	}
	if _, _, err := DecodeMessage([]byte(`{}`)); err == nil {
		t.Fatal("missing data array accepted")
	}
	raw := directRecord()
	raw["__scope_type"] = "DEFAULT"
	raw["__scope"] = nil
	b, _ := json.Marshal([]any{raw})
	if records, _, err := DecodeMessage(b); err != nil || len(records) != 0 {
		t.Fatal("null scope was accepted as an empty DEFAULT scope")
	}
}
