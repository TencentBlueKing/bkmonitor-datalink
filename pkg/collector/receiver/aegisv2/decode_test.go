package aegisv2

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

var sampleAegisPayload = loadTestPayload("testdata/resource.json")

func loadTestPayload(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return data
}

func TestDecodeCollect_FallbackOnlyForNonAegisJSON(t *testing.T) {
	for _, body := range []string{`{}`, `{"resourceSpans":[]}`} {
		traces, err := decodeCollect(define.ContentTypeJson, []byte(body))
		require.NoError(t, err)
		assert.Zero(t, traces.SpanCount())
	}
	for _, body := range []string{`{"topic":`, `{"topic":"SDK-test","bean":42}`, `{"topic":"SDK-test","d2":{}}`, `{"topic":"SDK-test","d2":[{"fields":"bad"}]}`} {
		_, err := decodeCollect(define.ContentTypeJson, []byte(body))
		require.Error(t, err)
	}
}

func TestDecodeCollect_EmptyAndUnavailableVitals(t *testing.T) {
	for _, tt := range []struct {
		name  string
		body  string
		spans int
	}{
		{"empty otlp", `{}`, 0},
		{"empty aegis", `{"topic":"SDK-test","d2":[]}`, 0},
		{"unavailable vitals", `{"topic":"SDK-test","d2":[{"fields":"{\"type\":\"web_vitals\"}","message":["{\"FCP\":-1,\"CLS\":-1}"]}]}`, 0},
		{"CLS only", `{"topic":"SDK-test","d2":[{"fields":"{\"type\":\"web_vitals\"}","message":["{\"CLS\":0.1}"]}]}`, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data, err := decodeCollect(define.ContentTypeJson, []byte(tt.body))
			require.NoError(t, err)
			assert.Equal(t, tt.spans, data.SpanCount())
		})
	}
}
