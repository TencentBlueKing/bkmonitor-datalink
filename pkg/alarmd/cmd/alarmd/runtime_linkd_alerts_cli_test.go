package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
)

// linkdSearchStub is the link's control plane: it answers the search with
// the given body and status and keeps every request body; any other path
// fails the test.
type linkdSearchStub struct {
	mu     sync.Mutex
	status int
	answer string
	bodies []string
	auth   []string
}

func (stub *linkdSearchStub) serve(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/alerts/search" {
			t.Errorf("linkd.alerts asked %s %s; only the search is its route", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		stub.mu.Lock()
		stub.bodies = append(stub.bodies, string(body))
		stub.auth = append(stub.auth, r.Header.Get("Internal-Token"))
		status, answer := stub.status, stub.answer
		stub.mu.Unlock()
		if status != 0 && status != http.StatusOK {
			http.Error(w, answer, status)
			return
		}
		_, _ = w.Write([]byte(answer))
	}
}

// linkdAlertsOperationAgainst builds linkd.alerts the way the SRE role does:
// the management client from the control-plane configuration, through
// linkdSearchOf, with the deployment's event source.
func linkdAlertsOperationAgainst(t *testing.T, stub *linkdSearchStub) obchannel.Operation {
	t.Helper()
	server := httptest.NewServer(stub.serve(t))
	t.Cleanup(server.Close)
	var cfg config.Config
	cfg.PhaseTwo.Linkd.ControlPlane = config.LinkdControlPlaneConfig{URL: server.URL, JWTSecret: "example-secret", EventSourceID: "source-a"}
	client, err := newLinkdManage(cfg, time.Now)
	if err != nil || client == nil {
		t.Fatalf("client %v (%v)", client, err)
	}
	return cliLinkdAlertsOperation(linkdSearchOf(client), cfg.PhaseTwo.Linkd.ControlPlane.Source())
}

func (stub *linkdSearchStub) requests() []map[string]any {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	var out []map[string]any
	for _, body := range stub.bodies {
		var request map[string]any
		_ = json.Unmarshal([]byte(body), &request)
		out = append(out, request)
	}
	return out
}

func runLinkdAlertsOp(t *testing.T, op obchannel.Operation, params obchannel.Params) obchannel.Outcome {
	t.Helper()
	if err := op.Validate(params); err != nil {
		t.Fatalf("params %v refused: %v", params, err)
	}
	return op.Run(context.Background(), params)
}

const linkdAlertsPage = `{"items":[{"alert_id":"a1","bk_tenant_id":"system","event_source_id":"source-a",` +
	`"fingerprint":"0123456789abcdef0123456789abcdef","status":"active","severity":"warning",` +
	`"labels":{"strategy_id":"101","strategy_version":7},"extra_data":{"password":"never-returned-value"}}],"next":"cursor-2"}`

// By strategy, the search names the tenant, this deployment's event source,
// the strategy and status active; the answer lifts the compared fields from
// the record, redacts the record and passes the next cursor back.
func TestLinkdAlertsByStrategyReadsTheActiveAlertsThroughTheSearch(t *testing.T) {
	stub := &linkdSearchStub{answer: linkdAlertsPage}
	out := runLinkdAlertsOp(t, linkdAlertsOperationAgainst(t, stub), obchannel.Params{"strategy_id": "101", "limit": json.Number("50")})
	if out.Error != nil {
		t.Fatalf("error %+v", out.Error)
	}
	requests := stub.requests()
	if len(requests) != 1 {
		t.Fatalf("searches %d, want 1", len(requests))
	}
	query, _ := json.Marshal(requests[0]["query"])
	for _, want := range []string{`{"term":{"event_source_id":"source-a"}}`, `{"term":{"labels.strategy_id":"101"}}`, `{"term":{"status":"active"}}`} {
		if !strings.Contains(string(query), want) {
			t.Fatalf("query %s lacks %s", query, want)
		}
	}
	if requests[0]["bk_tenant_id"] != "system" || requests[0]["limit"] != float64(50) {
		t.Fatalf("request %v: want tenant system, limit 50", requests[0])
	}
	if !strings.HasPrefix(stub.auth[0], "Bearer ") {
		t.Fatalf("Internal-Token %q: the search goes with the management token", stub.auth[0])
	}
	answer := out.Value.(linkdAlertsAnswer)
	if answer.Mode != "strategy" || answer.NextCursor != "cursor-2" || out.Complete || len(answer.Alerts) != 1 {
		t.Fatalf("answer %+v complete %v", answer, out.Complete)
	}
	record := answer.Alerts[0]
	if record.AlertID != "a1" || record.StrategyID != "101" || record.StrategyVersion != "7" || record.Status != "active" {
		t.Fatalf("record %+v", record)
	}
	if strings.Contains(string(record.Record), "never-returned-value") {
		t.Fatalf("record not redacted: %s", record.Record)
	}
}

// By alert or fingerprint the search reads any status: no status term, and
// the cursor asked is the one sent.
func TestLinkdAlertsByAlertOrFingerprintReadsAnyStatus(t *testing.T) {
	for _, params := range []obchannel.Params{
		{"alert_id": "a1", "cursor": "cursor-1", "tenant": "tenant-b"},
		{"fingerprint": "0123456789abcdef0123456789abcdef", "cursor": "cursor-1", "tenant": "tenant-b"},
	} {
		stub := &linkdSearchStub{answer: `{"items":[],"next":""}`}
		out := runLinkdAlertsOp(t, linkdAlertsOperationAgainst(t, stub), params)
		requests := stub.requests()
		if out.Error != nil || !out.Complete || len(requests) != 1 {
			t.Fatalf("%v: outcome %+v, searches %d", params, out, len(requests))
		}
		query, _ := json.Marshal(requests[0]["query"])
		if strings.Contains(string(query), `"status"`) || requests[0]["after"] != "cursor-1" || requests[0]["bk_tenant_id"] != "tenant-b" {
			t.Fatalf("%v: request %v query %s", params, requests[0], query)
		}
	}
}

// Exactly one way of asking; without a control plane the operation is
// listed and unavailable with its reason.
func TestLinkdAlertsAsksOneWay(t *testing.T) {
	op := cliLinkdAlertsOperation(linkdSearchOf(nil), "")
	for _, params := range []obchannel.Params{{}, {"strategy_id": "1", "alert_id": "a"}, {"alert_id": "a", "fingerprint": "0123456789abcdef0123456789abcdef"}} {
		if op.Validate(params) == nil {
			t.Fatalf("params %v accepted", params)
		}
	}
	if available := op.Availability(); available.Available || !strings.Contains(available.Reason, "linkd_control_plane_not_configured") {
		t.Fatalf("without a control plane: %+v", available)
	}
}

// A refusal comes back named by the link's status, with its own words.
func TestLinkdAlertsCarriesTheLinksRefusal(t *testing.T) {
	stub := &linkdSearchStub{status: http.StatusTooManyRequests, answer: "alert search busy"}
	out := runLinkdAlertsOp(t, linkdAlertsOperationAgainst(t, stub), obchannel.Params{"alert_id": "a1"})
	if out.Error == nil || out.Error.Code != "linkd_http_429" || !strings.Contains(out.Error.Message, "busy") {
		t.Fatalf("outcome %+v", out.Error)
	}
}
