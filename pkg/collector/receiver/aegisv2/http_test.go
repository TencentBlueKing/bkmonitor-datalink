package aegisv2

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

func withTestHTTPService(t *testing.T, publish func(r *define.Record)) {
	withTestHTTPServiceWithValidator(t, publish, func(r *define.Record) (define.StatusCode, string, error) {
		return define.StatusCodeOK, "", nil
	})
}

func withTestHTTPServiceWithValidator(
	t *testing.T,
	publish func(r *define.Record),
	validate define.PreCheckValidateFunc,
) {
	t.Helper()
	original := httpSvc
	httpSvc = httpService{
		Publisher: receiver.Publisher{Func: publish},
		Validator: pipeline.Validator{Func: validate},
	}
	t.Cleanup(func() {
		httpSvc = original
	})
}

func awaitPublishedRecordTypes(t *testing.T, published <-chan define.RecordType, count int) []define.RecordType {
	t.Helper()
	got := make([]define.RecordType, 0, count)
	deadline := time.After(2 * time.Second)
	for len(got) < count {
		select {
		case recordType := <-published:
			got = append(got, recordType)
		case <-deadline:
			t.Fatalf("timed out waiting for %d published records, got %d", count, len(got))
		}
	}
	return got
}

func TestWhitelistResponse_TimeFieldsAreNumbers(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, routeAegisV2Whitelist, nil)
	resp := httptest.NewRecorder()

	httpSvc.Whitelist(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)

	var body struct {
		Code            int                `json:"code"`
		Msg             string             `json:"msg"`
		IsInWhiteList   int                `json:"is_in_white_list"`
		SampleMap       whitelistSampleMap `json:"sample_map"`
		ServerTime      int64              `json:"server_time"`
		StartServerTime int64              `json:"start_server_time"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))

	assert.Equal(t, 0, body.Code)
	assert.Equal(t, "success", body.Msg)
	assert.Equal(t, 0, body.IsInWhiteList)
	assert.Equal(t, whitelistSample, body.SampleMap)
	assert.Greater(t, body.ServerTime, int64(0))
	assert.GreaterOrEqual(t, body.ServerTime, body.StartServerTime)
	assert.Equal(t, body.ServerTime, body.StartServerTime)
}

func TestWhitelistResponse_StartServerTimeIgnoresUIDTimestamp(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodGet,
		routeAegisV2Whitelist+"?uid=user_1781749136169_b838746b&topic=SDK-daffdasfdasfdsafdas",
		nil,
	)
	resp := httptest.NewRecorder()

	httpSvc.Whitelist(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)

	var body struct {
		ServerTime      int64 `json:"server_time"`
		StartServerTime int64 `json:"start_server_time"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))

	assert.Equal(t, body.ServerTime, body.StartServerTime)
	assert.GreaterOrEqual(t, body.ServerTime, body.StartServerTime)
}

func TestExportCollect_DerivesMetricsForWebVitals(t *testing.T) {
	published := make(chan define.RecordType, 2)
	withTestHTTPService(t, func(r *define.Record) {
		published <- r.RecordType
	})

	req := httptest.NewRequest(http.MethodPost, routeV2Collect, bytes.NewBufferString(`{
	"topic": "SDK-test",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0",
		"aid": "aid-http-1"
	},
	"d2": [
		{
			"fields": "{\"from\": \"https://example.com/index.html\", \"session\": {\"id\": \"sess-http-1\"}, \"view\": {\"id\": \"view-http-1\", \"loading_type\": \"initial_load\", \"view_name\": \"Test Page\", \"view_url\": \"https://example.com/index.html\"}, \"type\": \"web_vitals\", \"level\": \"info\", \"plugin\": \"webVitals\"}",
			"message": [
				"{\"msg\": \"web_vitals\", \"FCP\": 452, \"LCP\": 452, \"FID\": -1, \"CLS\": 0.467, \"INP\": -1, \"aegisv2_goto\": \"abc123\", \"timestamp\": 1782214911075}"
			]
		}
	]
}`))
	req.Header.Set(define.ContentType, define.ContentTypeJson)
	resp := httptest.NewRecorder()

	httpSvc.ExportCollect(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	assert.ElementsMatch(t,
		[]define.RecordType{define.RecordTraces, define.RecordMetrics},
		awaitPublishedRecordTypes(t, published, 2),
	)
}

func TestExportCollect_LeavesNonWebVitalsAsTracesOnly(t *testing.T) {
	published := make(chan define.RecordType, 2)
	withTestHTTPService(t, func(r *define.Record) {
		published <- r.RecordType
	})

	req := httptest.NewRequest(http.MethodPost, routeV2Collect, bytes.NewBufferString(`{
	"topic": "SDK-test",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0"
	},
	"d2": [
		{
			"fields": "{\"type\": \"pv\", \"plugin\": \"pv\"}",
			"message": [
				"{\"msg\": \"pv\", \"timestamp\": 1782214911075}"
			]
		}
	]
}`))
	req.Header.Set(define.ContentType, define.ContentTypeJson)
	resp := httptest.NewRecorder()

	httpSvc.ExportCollect(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, []define.RecordType{define.RecordTraces}, awaitPublishedRecordTypes(t, published, 1))

	select {
	case recordType := <-published:
		t.Fatalf("unexpected extra published record type: %s", recordType)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestExportCollect_MetricsPublishFailureDoesNotSilentlyPublish(t *testing.T) {
	published := make(chan define.RecordType, 2)
	withTestHTTPServiceWithValidator(
		t,
		func(r *define.Record) {
			published <- r.RecordType
		},
		func(r *define.Record) (define.StatusCode, string, error) {
			if r.RecordType == define.RecordMetrics {
				return define.StatusBadRequest, define.ProcessorProxyValidator, errors.New("metrics rejected")
			}
			return define.StatusCodeOK, "", nil
		},
	)

	req := httptest.NewRequest(http.MethodPost, routeV2Collect, bytes.NewBufferString(`{
	"topic": "SDK-test",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0",
		"aid": "aid-http-1"
	},
	"d2": [
		{
			"fields": "{\"from\": \"https://example.com/index.html\", \"session\": {\"id\": \"sess-http-1\"}, \"view\": {\"id\": \"view-http-1\", \"loading_type\": \"initial_load\", \"view_name\": \"Test Page\", \"view_url\": \"https://example.com/index.html\"}, \"type\": \"web_vitals\", \"level\": \"info\", \"plugin\": \"webVitals\"}",
			"message": [
				"{\"msg\": \"web_vitals\", \"FCP\": 452, \"LCP\": 452, \"FID\": -1, \"CLS\": 0.467, \"INP\": -1, \"aegisv2_goto\": \"abc123\", \"timestamp\": 1782214911075}"
			]
		}
	]
}`))
	req.Header.Set(define.ContentType, define.ContentTypeJson)
	resp := httptest.NewRecorder()

	httpSvc.ExportCollect(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, []define.RecordType{define.RecordTraces}, awaitPublishedRecordTypes(t, published, 1))

	select {
	case recordType := <-published:
		t.Fatalf("unexpected extra published record type: %s", recordType)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestProcessCollect_DerivesMetricsWithoutPanic(t *testing.T) {
	published := make(chan *define.Record, 2)
	withTestHTTPService(t, func(r *define.Record) {
		published <- r
	})

	body := []byte(`{
	"topic": "SDK-test",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0",
		"aid": "aid-http-1",
		"referer": "https://example.com/ref"
	},
	"d2": [
		{
			"fields": "{\"from\": \"https://example.com/index.html\", \"session\": {\"id\": \"sess-http-1\"}, \"view\": {\"id\": \"view-http-1\", \"loading_type\": \"initial_load\", \"view_name\": \"Test Page\", \"view_url\": \"https://example.com/index.html\"}, \"type\": \"web_vitals\", \"level\": \"info\", \"plugin\": \"webVitals\"}",
			"message": [
				"{\"msg\": \"web_vitals\", \"FCP\": 452, \"LCP\": 452, \"FID\": -1, \"CLS\": 0.467, \"INP\": -1, \"aegisv2_goto\": \"abc123\", \"timestamp\": 1782214911075}"
			]
		}
	]
}`)

	code, err := httpSvc.processCollect(
		"127.0.0.1",
		time.Now(),
		define.ContentTypeJson,
		"",
		nil,
		body,
	)

	require.NoError(t, err)
	assert.Equal(t, define.StatusCodeOK, code)

	first := <-published
	second := <-published

	assert.ElementsMatch(t,
		[]define.RecordType{define.RecordTraces, define.RecordMetrics},
		[]define.RecordType{first.RecordType, second.RecordType},
	)

	var metrics pmetric.Metrics
	if first.RecordType == define.RecordMetrics {
		metrics = first.Data.(pmetric.Metrics)
	} else {
		metrics = second.Data.(pmetric.Metrics)
	}

	require.Equal(t, 1, metrics.ResourceMetrics().Len())
	attrs := metrics.ResourceMetrics().At(0).Resource().Attributes()
	v, ok := attrs.Get("session.id")
	require.True(t, ok)
	assert.Equal(t, "sess-http-1", v.StringVal())
	v, ok = attrs.Get("referer")
	require.True(t, ok)
	assert.Equal(t, "https://example.com/ref", v.StringVal())
}
