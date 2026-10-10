package curl

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestURLDiagnosticsKeepAuthentication(t *testing.T) {
	metadata.InitMetadata()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer func() { _ = provider.Shutdown(context.Background()); otel.SetTracerProvider(previous) }()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "example-user" || password != "url-secret" || r.URL.Query().Get("token") != "query-secret" || r.Header.Get("Authorization-Custom") != "header-secret" {
			http.Error(w, "wrong credentials", 401)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	address := strings.Replace(server.URL, "http://", "http://example-user:url-secret@", 1) + "?token=query-secret&route=keep"
	ctx := metadata.InitHashID(context.Background())
	_, err := new(HttpCurl).Request(ctx, Get, Options{UrlPath: address, Headers: map[string]string{"Authorization-Custom": "header-secret"}, Timeout: time.Second}, new(any))
	require.NoError(t, err, "redaction must not change actual URL authentication")
	for _, span := range exporter.GetSpans() {
		text := fmt.Sprint(span.Attributes, span.Events, span.Status)
		for _, secret := range []string{"url-secret", "query-secret", "header-secret"} {
			require.NotContains(t, text, secret)
		}
	}
	requestErr := &url.Error{Op: "Get", URL: address, Err: errors.New("connection refused")}
	err = HandleClientError(ctx, metadata.MsgHttpCurl, address, requestErr)
	require.NotContains(t, err.Error(), "url-secret")
	require.NotContains(t, err.Error(), "query-secret")
	_, err = new(HttpCurl).Request(ctx, Get, Options{UrlPath: "http://[bad-url?token=query-secret"}, new(any))
	require.Error(t, err)
	require.NotContains(t, err.Error(), "query-secret")
}
