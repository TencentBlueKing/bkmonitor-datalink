package aegisv2

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func TestD2MessageTimeRange(t *testing.T) {
	t.Run("falls back to relative first screen timing", func(t *testing.T) {
		msg := d2Message{
			Timestamp: 1_781_685_646_787,
			raw: map[string]any{
				"firstScreenTiming": 912.0,
			},
		}

		start, end := msg.TimeRange(pcommon.NewTimestampFromTime(time.UnixMilli(0)))
		assert.Equal(t, msg.Timestamp, end.AsTime().UnixMilli())
		assert.Equal(t, msg.Timestamp-912, start.AsTime().UnixMilli())
	})

	t.Run("treats absolute first screen timing as end timestamp", func(t *testing.T) {
		msg := d2Message{
			Timestamp: 1_781_685_646_000,
			raw: map[string]any{
				"firstScreenTiming": 1_781_685_646_429.0,
			},
		}

		start, end := msg.TimeRange(pcommon.NewTimestampFromTime(time.UnixMilli(0)))
		assert.Equal(t, int64(1_781_685_646_000), start.AsTime().UnixMilli())
		assert.Equal(t, int64(1_781_685_646_429), end.AsTime().UnixMilli())
	})

	t.Run("uses page performance phase totals before first screen fallback", func(t *testing.T) {
		msg := d2Message{
			Timestamp:        1_781_685_646_787,
			TTFB:             52,
			ContentDownload:  1,
			DOMParse:         517,
			ResourceDownload: 181,
			raw: map[string]any{
				"firstScreenTiming": 912.0,
			},
		}

		start, end := msg.TimeRange(pcommon.NewTimestampFromTime(time.UnixMilli(0)))
		assert.Equal(t, msg.Timestamp, end.AsTime().UnixMilli())
		assert.Equal(t, msg.Timestamp-751, start.AsTime().UnixMilli())
	})
}

func TestAegisEventIsError(t *testing.T) {
	tests := []struct {
		name   string
		level  string
		plugin string
		msgRaw map[string]any
		want   bool
	}{
		{name: "exact error level", level: "error", want: true},
		{name: "critical level", level: "critical", want: true},
		{name: "panic level", level: "panic", want: true},
		{name: "exception level", level: "exception", want: true},
		{name: "false positive no error avoided", level: "no_error", want: false},
		{name: "false positive error bypass avoided", level: "error_bypass", want: false},
		{name: "plugin error still wins", level: "info", plugin: "error", want: true},
		{name: "websocket success flag false is error", plugin: "websocket", msgRaw: map[string]any{"successFlag": false}, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := aegisEvent{
				record: d2Record{Fields: d2Fields{Level: tc.level, Plugin: tc.plugin}},
				msg:    d2Message{raw: tc.msgRaw},
			}
			assert.Equal(t, tc.want, e.IsError())
		})
	}
}

func TestAegisEventSpanKindUsesAPIHelper(t *testing.T) {
	e := aegisEvent{record: d2Record{Fields: d2Fields{Type: "api"}}}
	kind, ok := e.SpanKind()
	require.True(t, ok)
	assert.Equal(t, ptrace.SpanKindClient, kind)
}

func TestD2RecordUnmarshalJSON(t *testing.T) {
	t.Run("object payload", func(t *testing.T) {
		var record d2Record
		err := json.Unmarshal([]byte(`{
			"fields":{"type":"session","session":{"id":"session-1"}},
			"message":[{"msg":"session","timestamp":123}]
		}`), &record)
		require.NoError(t, err)
		assert.Equal(t, "session", record.Fields.Type)
		assert.Equal(t, "session-1", record.Fields.Session.ID)
		require.Len(t, record.Message, 1)
		assert.Equal(t, "session", record.Message[0].Msg)
		assert.Equal(t, int64(123), record.Message[0].Timestamp)
	})

	t.Run("string wrapped payload", func(t *testing.T) {
		var record d2Record
		err := json.Unmarshal([]byte(`{
			"fields":"{\"type\":\"session\",\"session\":{\"id\":\"session-2\"}}",
			"message":["{\"msg\":\"session\",\"timestamp\":456}"]
		}`), &record)
		require.NoError(t, err)
		assert.Equal(t, "session", record.Fields.Type)
		assert.Equal(t, "session-2", record.Fields.Session.ID)
		require.Len(t, record.Message, 1)
		assert.Equal(t, "session", record.Message[0].Msg)
		assert.Equal(t, int64(456), record.Message[0].Timestamp)
	})
}

func TestSetPageURLAttrsFallsBackOnParseError(t *testing.T) {
	attrs := pcommon.NewMap()
	setPageURLAttrs(attrs, "https://example.com/%zz/path")

	v, ok := attrs.Get("rum.page.path")
	require.True(t, ok)
	assert.Equal(t, "/%zz/path", v.StringVal())
	v, ok = attrs.Get("view.url_path_group")
	require.True(t, ok)
	assert.Equal(t, "/%zz/path", v.StringVal())
}

func TestPathFromURLFallsBackOnParseError(t *testing.T) {
	assert.Equal(t, "/%zz/path", pathFromURL("https://example.com/%zz/path"))
}

func TestDecodeTracesKeepsSessionIDOnSpanOnly(t *testing.T) {
	buf := []byte(`{
		"topic":"SDK-xxxxx",
		"scheme":"v2",
		"bean":{"version":"1.0.0"},
		"d2":[{
			"fields":{"type":"session","plugin":"session","session":{"id":"session-1"}},
			"message":[{"msg":"session","timestamp":123}]
		}]
	}`)

	traces, handled, err := decodeTraces(buf)
	require.True(t, handled)
	require.NoError(t, err)

	rs := traces.ResourceSpans().At(0)
	v, ok := rs.Resource().Attributes().Get("session.id")
	require.True(t, ok)
	assert.Equal(t, "session-1", v.StringVal())

	span := rs.ScopeSpans().At(0).Spans().At(0)
	v, ok = span.Attributes().Get("session.id")
	require.True(t, ok)
	assert.Equal(t, "session-1", v.StringVal())
}
