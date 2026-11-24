package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/config"
	"linkd/internal/ingestpreview"
)

type ingestPreviewStub struct{ calls int }

func (s *ingestPreviewStub) Preview(context.Context, ingestpreview.Request) (ingestpreview.Response, error) {
	s.calls++
	return ingestpreview.Response{ConfigOrigin: "draft", Items: []ingestpreview.Item{}}, nil
}

func TestIngestPreviewAuthenticationAndStrictInput(t *testing.T) {
	stub := &ingestPreviewStub{}
	handler := (&API{IngestPreviewer: stub, Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}, WorkerToken: "worker"}}).Handler()
	for _, tc := range []struct {
		name, body, token string
		status            int
	}{
		{"no auth", `{}`, "", 401},
		{"valid request", `{"source":{"kind":"draft","calculation":{"cleaner":{"type":"standard"}}}}`, testJWT(t, "admin"), 200},
		{"enrich forbidden", `{"source":{"kind":"draft","calculation":{"enrich":{}}}}`, testJWT(t, "admin"), 400},
		{"mq forbidden", `{"source":{"kind":"draft","calculation":{"storage":{}}}}`, testJWT(t, "admin"), 400},
		{"oversized", `{"session_id":"` + strings.Repeat("x", 1<<20) + `"}`, testJWT(t, "admin"), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/ingest/preview", strings.NewReader(tc.body))
			req.Header.Set("Internal-Token", tc.token)
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, req)
			if out.Code != tc.status {
				t.Fatalf("status=%d body=%s", out.Code, out.Body.String())
			}
		})
	}
	if stub.calls != 1 {
		t.Fatalf("invalid request reached computation calls=%d", stub.calls)
	}
}
