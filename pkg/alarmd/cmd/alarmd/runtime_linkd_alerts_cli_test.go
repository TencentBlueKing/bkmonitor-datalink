package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// linkdAlertsConsole is a Console that lists the test target (source
// "source"), reconciles strategy 101 and lists alerts, logs and events from
// the given pages; every query it was asked is kept by path.
func linkdAlertsConsole(t *testing.T, pages map[string]any, refuse int) (*openalerts.HTTPReconciler, *sync.Map) {
	t.Helper()
	asked := &sync.Map{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, testLinkdAPIRoot)
		asked.Store(path, r.URL.Query())
		switch path {
		case "/strategy-index/targets":
			_ = json.NewEncoder(w).Encode([]openalerts.TargetBinding{consoleTestBinding()})
		case "/strategy-index/reconcile":
			b := consoleTestBinding()
			q := r.URL.Query()
			_ = json.NewEncoder(w).Encode(map[string]any{"target": b, "tenantId": q.Get("bk_tenant_id"), "strategyId": q.Get("strategy_id"),
				"key": b.KeyPrefix + ":" + q.Get("bk_tenant_id") + ":" + q.Get("strategy_id"), "complete": true,
				"redis": map[string]any{"complete": true}, "alerts": map[string]any{"complete": true},
				"rows": []any{
					map[string]any{"fingerprint": "f1", "status": "matched", "alerts": []openalerts.Alert{{AlertID: "a1", EventSourceID: "source", Fingerprint: "f1", Severity: "critical"}}},
					map[string]any{"fingerprint": "f2", "status": "missing_redis", "alerts": []openalerts.Alert{{AlertID: "a2", EventSourceID: "source", Fingerprint: "f2"}}},
					map[string]any{"fingerprint": "f3", "status": "redis_only", "alerts": []openalerts.Alert{}},
				}})
		case "/alerts", "/alert-logs", "/events":
			if refuse != 0 {
				w.WriteHeader(refuse)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "invalid_query", "message": "query range must not exceed 604800 seconds"}})
				return
			}
			_ = json.NewEncoder(w).Encode(pages[path])
		default:
			if !strings.HasPrefix(path, "/alerts/") {
				http.NotFound(w, r)
				return
			}
			if refuse != 0 {
				w.WriteHeader(refuse)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "invalid_query", "message": "query range must not exceed 604800 seconds"}})
				return
			}
			if page, ok := pages[path]; ok {
				_ = json.NewEncoder(w).Encode(page)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"对象不存在","requestId":"req-1"}}`))
		}
	}))
	t.Cleanup(server.Close)
	console, err := openalerts.NewHTTPReconciler(openalerts.HTTPReconcilerOptions{BaseURL: server.URL + testLinkdAPIRoot, Client: server.Client(),
		Username: "user", Password: "secret", MaxResponseBytes: 1 << 20,
		Index: openalerts.IndexLocation{KeyPrefix: "test:active", Address: "192.0.2.10:6379", Database: 3}})
	if err != nil {
		t.Fatal(err)
	}
	return console, asked
}

func runLinkdAlerts(t *testing.T, console linkdConsoleReader, params obchannel.Params) obchannel.Outcome {
	t.Helper()
	op := cliLinkdAlertsOperation(console)
	if err := op.Validate(params); err != nil {
		t.Fatalf("valid params refused: %v", err)
	}
	return readLinkdAlerts(consoleCtx(), console, params, func() time.Time { return time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC) })
}

// By strategy, the read is the link's reconciliation of the strategy for the
// binding's source and hook: the fingerprints in the set with an active
// alert, those the set lacks, those it holds with none, and the alerts.
func TestLinkdAlertsByStrategyIsTheLinksReconciliation(t *testing.T) {
	console, asked := linkdAlertsConsole(t, nil, 0)
	out := runLinkdAlerts(t, console, obchannel.Params{"strategy_id": "101"})
	if out.Error != nil {
		t.Fatalf("error %+v", out.Error)
	}
	answer := out.Value.(linkdAlertsAnswer)
	s := answer.Strategy
	if answer.Mode != "strategy" || answer.EventSourceID != "source" || answer.TenantID != "system" || s == nil ||
		strings.Join(s.Members, ",") != "f1,f2" || strings.Join(s.MissingRedis, ",") != "f2" || strings.Join(s.RedisOnly, ",") != "f3" || len(s.Alerts) != 2 {
		t.Fatalf("answer %+v strategy %+v", answer, s)
	}
	q, _ := asked.Load("/strategy-index/reconcile")
	query := q.(url.Values)
	if query.Get("event_source_id") != "source" || query.Get("hook_name") != "active" || query.Get("strategy_id") != "101" || query.Get("bk_tenant_id") != "system" {
		t.Fatalf("reconcile asked %v", query)
	}
	if len(out.Limitations) == 0 || !strings.Contains(out.Limitations[0], "does not mean closed") {
		t.Fatalf("limitations %v: an empty strategy must not read as closed", out.Limitations)
	}
}

// By alert, the alert page is asked with the binding's source, the identity
// and the time range as given (24 hours back by default), and each alert's
// logs and events are read; what a reader compares is lifted from the
// record, the records are redacted, and every cursor is passed back.
func TestLinkdAlertsByAlertReadsTheRecordItsLogsAndEvents(t *testing.T) {
	alert := map[string]any{"tenantId": "system", "id": "a1", "timestamp": "2026-10-16T07:00:00Z", "summary": map[string]any{"status": "recovered"},
		"payload": map[string]any{"alert_id": "a1", "status": "recovered", "event_source_id": "source", "severity": "critical",
			"labels": map[string]any{"strategy_id": 101, "strategy_version": "7"},
			"enrich": map[string]any{"status": "partial", "diagnostics": []any{"invalid_field(subject_id)"}},
			"extra":  map[string]any{"password": "never-returned-value"}}}
	pages := map[string]any{
		"/alerts":     map[string]any{"items": []any{alert}, "nextCursor": "alerts-next", "source": "elasticsearch"},
		"/alert-logs": map[string]any{"items": []any{map[string]any{"id": "l1", "payload": map[string]any{"operation_kind": "recover"}}}, "nextCursor": "logs-next", "source": "mysql"},
		"/events":     map[string]any{"items": []any{map[string]any{"id": "e1", "payload": map[string]any{"state": "accepted", "outcome": "alert_recovered"}}}, "source": "elasticsearch"},
	}
	console, asked := linkdAlertsConsole(t, pages, 0)
	out := runLinkdAlerts(t, console, obchannel.Params{"source_alert_id": "sa-1", "limit": json.Number("20"), "cursor": "c0"})
	if out.Error != nil {
		t.Fatalf("error %+v", out.Error)
	}
	answer := out.Value.(linkdAlertsAnswer)
	if answer.Mode != "alert" || answer.From != "2026-10-15T08:00:00Z" || answer.To != "2026-10-16T08:00:00Z" || answer.NextCursor != "alerts-next" || len(answer.Alerts) != 1 {
		t.Fatalf("answer %+v", answer)
	}
	q, _ := asked.Load("/alerts")
	query := q.(url.Values)
	if query.Get("source_alert_id") != "sa-1" || query.Get("event_source_id") != "source" || query.Get("bk_tenant_id") != "system" ||
		query.Get("limit") != "20" || query.Get("cursor") != "c0" || query.Get("from") != "2026-10-15T08:00:00Z" || query.Get("to") != "2026-10-16T08:00:00Z" {
		t.Fatalf("alerts asked %v", query)
	}
	logs, _ := asked.Load("/alert-logs")
	events, _ := asked.Load("/events")
	if logs.(url.Values).Get("alert_id") != "a1" || events.(url.Values).Get("related_alert_id") != "a1" || logs.(url.Values).Get("limit") != "" {
		t.Fatalf("logs asked %v events asked %v", logs, events)
	}
	record := answer.Alerts[0]
	if record.AlertID != "a1" || record.Status != "recovered" || record.StrategyID != "101" || record.StrategyVersion != "7" || record.EventSourceID != "source" ||
		!strings.Contains(string(record.Enrich), "invalid_field(subject_id)") || len(record.Logs) != 1 || record.LogsNextCursor != "logs-next" ||
		len(record.Events) != 1 || !strings.Contains(string(record.Events[0]), "alert_recovered") {
		t.Fatalf("record %+v", record)
	}
	encoded, _ := json.Marshal(answer)
	if strings.Contains(string(encoded), "never-returned-value") {
		t.Fatalf("a credential-named value came back: %s", encoded)
	}
	if out.Complete {
		t.Fatal("an answer with a next page reported complete")
	}
}

// An alert_id is read on the link's route for that one alert, never the
// list: the list takes no alert id and answers every alert in range, page
// after page. The alert's logs and events keep their cursors; there is no
// alert page to continue.
func TestLinkdAlertsByAlertIDReadsThatAlertOnly(t *testing.T) {
	alert := map[string]any{"tenantId": "system", "id": "a7",
		"payload": map[string]any{"alert_id": "a7", "status": "active", "event_source_id": "source", "labels": map[string]any{"strategy_id": "136"}}}
	pages := map[string]any{
		"/alerts/a7":  alert,
		"/alerts":     map[string]any{"items": []any{map[string]any{"tenantId": "system", "id": "other", "payload": map[string]any{"alert_id": "other"}}}, "nextCursor": "more"},
		"/alert-logs": map[string]any{"items": []any{}, "nextCursor": "logs-next"},
		"/events":     map[string]any{"items": []any{}, "nextCursor": "events-next"},
	}
	console, asked := linkdAlertsConsole(t, pages, 0)
	out := runLinkdAlerts(t, console, obchannel.Params{"alert_id": "a7", "logs_cursor": "l0", "events_cursor": "e0"})
	if out.Error != nil {
		t.Fatalf("error %+v", out.Error)
	}
	answer := out.Value.(linkdAlertsAnswer)
	if len(answer.Alerts) != 1 || answer.Alerts[0].AlertID != "a7" || answer.Alerts[0].StrategyID != "136" || answer.NextCursor != "" || !out.Complete {
		t.Fatalf("answer %+v complete %v", answer, out.Complete)
	}
	if _, listed := asked.Load("/alerts"); listed {
		t.Fatal("the alert list was asked for an alert_id")
	}
	q, _ := asked.Load("/alerts/a7")
	if q == nil || q.(url.Values).Get("bk_tenant_id") != "system" {
		t.Fatalf("alert route asked %v", q)
	}
	logs, _ := asked.Load("/alert-logs")
	events, _ := asked.Load("/events")
	if logs.(url.Values).Get("cursor") != "l0" || events.(url.Values).Get("cursor") != "e0" ||
		answer.Alerts[0].LogsNextCursor != "logs-next" || answer.Alerts[0].EventsNextCursor != "events-next" {
		t.Fatalf("logs asked %v events asked %v record %+v", logs, events, answer.Alerts[0])
	}

	missing := runLinkdAlerts(t, console, obchannel.Params{"alert_id": "gone"})
	if missing.Error != nil || len(missing.Value.(linkdAlertsAnswer).Alerts) != 0 || !missing.Complete ||
		len(missing.Limitations) == 0 || !strings.Contains(missing.Limitations[0], "no alert") {
		t.Fatalf("missing alert: %+v", missing)
	}
}

// Exactly one way of asking; a segment cursor belongs to one alert.
func TestLinkdAlertsAsksOneWay(t *testing.T) {
	op := cliLinkdAlertsOperation(nil)
	for _, params := range []obchannel.Params{{}, {"strategy_id": "1", "alert_id": "a"}, {"alert_id": "a", "source_alert_id": "b"},
		{"source_alert_id": "b", "logs_cursor": "x"}, {"alert_id": "a", "cursor": "x"}} {
		if op.Validate(params) == nil {
			t.Fatalf("params %v accepted", params)
		}
	}
	if op.Validate(obchannel.Params{"alert_id": "a", "events_cursor": "x"}) != nil {
		t.Fatal("an alert's events cursor refused")
	}
	if available := op.Availability(); available.Available || !strings.Contains(available.Reason, "linkd_console_not_configured") {
		t.Fatalf("without a Console: %+v", available)
	}
}

// The time range and page size are the link's to accept: a refusal comes
// back with its own words and is counted as a status failure.
func TestLinkdAlertsCarriesTheLinksRefusal(t *testing.T) {
	console, _ := linkdAlertsConsole(t, nil, http.StatusBadRequest)
	out := runLinkdAlerts(t, console, obchannel.Params{"alert_id": "a1", "from": "2026-01-01T00:00:00Z"})
	if out.Error == nil || out.Error.Code != "linkd_"+openalerts.ConsoleFailureStatus || !strings.Contains(out.Error.Message, "604800") || !strings.Contains(out.Error.Message, "400") {
		t.Fatalf("outcome %+v", out.Error)
	}
	call := console.Record().Calls[openalerts.ConsoleOpAlertRead]
	if call.Calls != 1 || call.Failures != 1 {
		t.Fatalf("alert_read call %+v", call)
	}
}
