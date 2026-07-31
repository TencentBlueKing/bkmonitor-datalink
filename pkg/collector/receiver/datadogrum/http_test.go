package datadogrum

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

type recordingBody struct {
	io.Reader
	closed bool
}

func (b *recordingBody) Close() error {
	b.closed = true
	return nil
}

func TestExportPublishesTraceRecord(t *testing.T) {
	body := &recordingBody{Reader: bytes.NewBufferString(`{"type":"view","date":1700000000000,"application":{"id":"app","name":"shop"},"session":{"id":"session","type":"user"},"view":{"id":"view","url":"https://example.com"}}`)}
	request := httptest.NewRequest(http.MethodPost, "/api/v2/rum?X-BK-TOKEN=test-token", body)
	request.RemoteAddr = "192.0.2.10:1234"
	response := httptest.NewRecorder()

	var published *define.Record
	service := HttpService{
		Publisher: receiver.Publisher{Func: func(record *define.Record) { published = record }},
		Validator: pipeline.Validator{Func: func(*define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		}},
	}

	service.Export(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.NotNil(t, published)
	assert.Equal(t, define.RecordRum, published.RecordType)
	assert.Equal(t, "192.0.2.10", published.RequestClient.IP)
	traces, ok := published.Data.(ptrace.Traces)
	require.True(t, ok)
	require.Equal(t, 1, traces.ResourceSpans().Len())
	assert.Equal(t, "page.view", traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Name())
	assert.True(t, body.closed)
}
