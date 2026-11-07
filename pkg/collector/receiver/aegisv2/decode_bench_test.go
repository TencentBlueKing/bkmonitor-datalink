// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package aegisv2

import (
	"encoding/json"
	"fmt"
	"testing"
)

const benchLargeExtFieldCount = 256

var benchTracePayloadLargeKnown = mustBuildBenchTracePayload("assets_speed", "asset_speed", benchLargeExtFieldCount)
var benchTracePayloadLargeUnknown = mustBuildBenchTracePayload("custom", "custom.event", benchLargeExtFieldCount)

func mustBuildBenchTracePayload(eventType, msg string, extraFieldCount int) []byte {
	message := map[string]any{
		"msg":              msg,
		"url":              "http://127.0.0.1:8080/styles.css",
		"status":           200,
		"assetType":        "css",
		"duration":         47.3,
		"domainLookup":     0,
		"connectTime":      0,
		"tlsTime":          139.4,
		"tcpAndRequestGap": 42.2,
		"requestTime":      1.4,
		"responseTime":     3.7,
		"timestamp":        int64(1781593481995),
		"nested": map[string]any{
			"kv": map[string]any{
				"k1": "v1",
				"k2": "v2",
			},
			"flags": []any{true, false, "x"},
		},
	}
	for i := 0; i < extraFieldCount; i++ {
		message[fmt.Sprintf("ext_field_%03d", i)] = fmt.Sprintf("value_%03d", i)
	}

	fieldsJSON, _ := json.Marshal(map[string]any{
		"from":    "http://127.0.0.1:8080/index.html",
		"session": map[string]any{"id": "session-1"},
		"view": map[string]any{
			"id":           "view-1",
			"view_name":    "VideoFlow",
			"view_url":     "http://127.0.0.1:8080/index.html",
			"loading_type": "initial_load",
		},
		"type":   eventType,
		"level":  "info",
		"plugin": "assetSpeed",
	})
	msgJSON, _ := json.Marshal(message)

	payload := map[string]any{
		"topic":  "SDK-xxxxx",
		"scheme": "v2",
		"bean": map[string]any{
			"version": "1.0.0",
			"referer": "http://127.0.0.1:8080/index.html",
		},
		"d2": []any{map[string]any{
			"fields":  string(fieldsJSON),
			"message": []any{string(msgJSON)},
		}},
	}

	b, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return b
}

func runDecodeTracesBenchmark(b *testing.B, payload []byte) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := decodeCollect("", payload)
		if err != nil {
			b.Fatalf("decodeCollect failed: err=%v", err)
		}
	}
}

func BenchmarkDecodeTraces(b *testing.B) {
	runDecodeTracesBenchmark(b, sampleAegisPayload)
}

func BenchmarkDecodeTraces_LargeExtKnown(b *testing.B) {
	runDecodeTracesBenchmark(b, benchTracePayloadLargeKnown)
}

func BenchmarkDecodeTraces_LargeExtUnknownFallback(b *testing.B) {
	runDecodeTracesBenchmark(b, benchTracePayloadLargeUnknown)
}
