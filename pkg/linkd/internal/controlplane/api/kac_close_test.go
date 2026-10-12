package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	lifecycleprocess "linkd/internal/lifecycle/process"
)

type kacCloserStub struct {
	command lifecycle.CloseAlertCommand
	alert   domain.Alert
	err     error
}

func (s *kacCloserStub) CloseKACAlert(_ context.Context, command lifecycle.CloseAlertCommand) (lifecycle.CloseAlertResult, error) {
	s.command = command
	return lifecycle.CloseAlertResult{Alert: s.alert}, s.err
}

func (s *kacCloserStub) GetAlert(_ context.Context, tenantID, alertID string) (domain.Alert, error) {
	if tenantID != s.alert.BKTenantID || alertID != s.alert.AlertID {
		return domain.Alert{}, &lifecycleprocess.CloseError{Status: 404, Code: "not_found", Message: "alert not found"}
	}
	return s.alert, nil
}

func TestKACCloseBoundaryAndResultLookup(t *testing.T) {
	stub := &kacCloserStub{alert: domain.Alert{BKTenantID: "tenant-a", AlertID: "alert-a", Status: domain.AlertStatusClosed, EndOperation: &domain.AlertEndOperation{ID: "operation-a"}, Content: "private alert payload"}}
	api := &API{Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}, WorkerToken: "worker"}, KACCloser: stub}
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		return w
	}
	body := `{"bk_tenant_id":"tenant-a","operation_id":"operation-a","operator_kind":"system","operator_id":"kac","operation_source":"system","reason":"auto close","effective_at":"2026-09-23T00:00:00Z"}`
	path := "/api/v1/integrations/kac/alerts/alert-a/close"
	if w := request(http.MethodGet, "/api/v1/runtime", "", ""); w.Code != 401 {
		t.Fatalf("management endpoint without token = %d", w.Code)
	}
	if w := request(http.MethodGet, "/internal/settings/severity", "", ""); w.Code != 401 {
		t.Fatalf("worker endpoint without token = %d", w.Code)
	}
	if w := request(http.MethodPut, "/api/v1/integrations/kac/alerts/alert-a", "", ""); w.Code != 401 {
		t.Fatalf("unregistered KAC method without token = %d", w.Code)
	}
	if w := request(http.MethodPost, path, "", body); w.Code != 200 || stub.command.OperatorKind != domain.OperatorKindSystem || stub.command.OperationSource != "system" || stub.command.OperationID != "operation-a" || !strings.Contains(w.Body.String(), `"status":"closed"`) || strings.Contains(w.Body.String(), "private alert payload") {
		t.Fatalf("close = %d %s, command=%#v", w.Code, w.Body.String(), stub.command)
	}
	if w := request(http.MethodGet, "/api/v1/integrations/kac/alerts/alert-a?bk_tenant_id=tenant-a", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"close_operation_id":"operation-a"`) || strings.Contains(w.Body.String(), "private alert payload") {
		t.Fatalf("lookup = %d %s", w.Code, w.Body.String())
	}
	if w := request(http.MethodGet, "/api/v1/integrations/kac/alerts/alert-a?bk_tenant_id=other", "", ""); w.Code != 404 {
		t.Fatalf("cross tenant lookup = %d", w.Code)
	}
	stub.err = &lifecycleprocess.CloseError{Status: 409, Code: "invalid_transition", Message: "state changed"}
	w := request(http.MethodPost, path, "", body)
	var failure struct {
		ErrorCode string `json:"error_code"`
	}
	if w.Code != 409 || json.Unmarshal(w.Body.Bytes(), &failure) != nil || failure.ErrorCode != "invalid_transition" {
		t.Fatalf("conflict = %d %s", w.Code, w.Body.String())
	}
}
