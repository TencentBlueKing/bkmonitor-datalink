package aegisv2

import (
	"encoding/json"
	"fmt"
	"testing"
)

const benchLargeExtFieldCount = 256

var benchTracePayload = []byte(`{
	"topic":"SDK-xxxxx",
	"scheme":"v2",
	"bean":{"version":"1.0.0","referer":"http://127.0.0.1:8080/index.html"},
	"d2":[{
		"fields":{
			"from":"http://127.0.0.1:8080/index.html",
			"session":{"id":"session-1"},
			"view":{"id":"view-1","view_name":"VideoFlow","view_url":"http://127.0.0.1:8080/index.html","loading_type":"initial_load"},
			"type":"assets_speed",
			"level":"info",
			"plugin":"assetSpeed"
		},
		"message":[{
			"msg":"asset_speed",
			"url":"http://127.0.0.1:8080/styles.css",
			"status":200,
			"assetType":"css",
			"duration":47.3,
			"domainLookup":0,
			"connectTime":0,
			"tlsTime":139.4,
			"tcpAndRequestGap":42.2,
			"requestTime":1.4,
			"responseTime":3.7,
			"timestamp":1781593481995
		}]
	}]
}`)

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

	payload := map[string]any{
		"topic":  "SDK-xxxxx",
		"scheme": "v2",
		"bean": map[string]any{
			"version": "1.0.0",
			"referer": "http://127.0.0.1:8080/index.html",
		},
		"d2": []any{map[string]any{
			"fields": map[string]any{
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
			},
			"message": []any{message},
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
		_, handled, err := decodeTraces(payload)
		if !handled || err != nil {
			b.Fatalf("decodeTraces failed: handled=%v err=%v", handled, err)
		}
	}
}

func BenchmarkDecodeTraces(b *testing.B) {
	runDecodeTracesBenchmark(b, benchTracePayload)
}

func BenchmarkDecodeTraces_LargeExtKnown(b *testing.B) {
	runDecodeTracesBenchmark(b, benchTracePayloadLargeKnown)
}

func BenchmarkDecodeTraces_LargeExtUnknownFallback(b *testing.B) {
	runDecodeTracesBenchmark(b, benchTracePayloadLargeUnknown)
}
