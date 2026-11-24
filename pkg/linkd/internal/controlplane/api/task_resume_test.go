package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/config"
	"linkd/internal/eventsource"
)

type resumeTestController struct {
	Controller
	calls int
	err   error
}

func (c *resumeTestController) ResumeTask(_ context.Context, source, task string, version, epoch int64) error {
	c.calls++
	if source != "source" || task != "source:lifecycle:0" || version != 7 || epoch != 42 {
		return eventsource.ErrConflict
	}
	return c.err
}

func TestTaskResumeManagementAuthenticationAndGeneration(t *testing.T) {
	for _, test := range []struct {
		name, token, body string
		err               error
		status, calls     int
	}{
		{"management", "admin", `{"expected_source_version":7,"expected_epoch":42}`, nil, 204, 1},
		{"missing auth", "", `{"expected_source_version":7,"expected_epoch":42}`, nil, 401, 0},
		{"worker token", "worker", `{"expected_source_version":7,"expected_epoch":42}`, nil, 401, 0},
		{"missing version", "admin", `{"expected_epoch":42}`, nil, 400, 0},
		{"unknown field", "admin", `{"expected_source_version":7,"expected_epoch":42,"force":true}`, nil, 400, 0},
		{"stale epoch", "admin", `{"expected_source_version":7,"expected_epoch":41}`, nil, 409, 1},
		{"not stopped", "admin", `{"expected_source_version":7,"expected_epoch":42}`, eventsource.ErrConflict, 409, 1},
		{"not found", "admin", `{"expected_source_version":7,"expected_epoch":42}`, eventsource.ErrNotFound, 404, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			controller := &resumeTestController{err: test.err}
			handler := (&API{Controller: controller, Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}, WorkerToken: "worker"}}).Handler()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/event-sources/source/tasks/source:lifecycle:0/resume", strings.NewReader(test.body))
			if test.token == "worker" {
				req.Header.Set("Authorization", "Bearer worker")
			} else if test.token != "" {
				req.Header.Set("Internal-Token", testJWT(t, test.token))
			}
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, req)
			if out.Code != test.status || controller.calls != test.calls {
				t.Fatalf("status=%d calls=%d", out.Code, controller.calls)
			}
		})
	}
}
