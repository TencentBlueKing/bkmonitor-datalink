// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package kaccompat

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/store/storetest"
)

func TestProjectionSeparatesSourceStateFromDisposalState(t *testing.T) {
	cases := []struct {
		name       string
		status     domain.AlertStatus
		endType    domain.AlertEndType
		source     string
		wantAction string
		wantSource string
	}{
		{"active", domain.AlertStatusActive, "", "", "firing", "firing"},
		{"manual", domain.AlertStatusClosed, domain.AlertEndTypeUser, "manual", "close", "firing"},
		{"legacy manual", domain.AlertStatusClosed, domain.AlertEndTypeUser, "", "close", "firing"},
		{"auto policy", domain.AlertStatusClosed, domain.AlertEndTypeSystem, "auto_policy", "close", "firing"},
		{"self heal", domain.AlertStatusClosed, domain.AlertEndTypeSystem, "self_heal", "close", "firing"},
		{"work order", domain.AlertStatusClosed, domain.AlertEndTypeUser, "work_order", "close", "firing"},
		{"system", domain.AlertStatusClosed, domain.AlertEndTypeSystem, "system", "close", "firing"},
		{"severity upgrade", domain.AlertStatusClosed, domain.AlertEndTypeSeverityUpgrade, "", "close", "firing"},
		{"source close", domain.AlertStatusClosed, domain.AlertEndTypeSource, "", "close", "close"},
		{"source recovery", domain.AlertStatusRecovered, domain.AlertEndTypeSource, "", "resolved", "resolved"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			alert := storetest.Alert("tenant-a", "alert", "opening-event", "fp", "warning")
			alert.Status, alert.EndType = tc.status, tc.endType
			if tc.status != domain.AlertStatusActive {
				alert.EndAt, alert.EndReason = &alert.UpdateAt, "maintenance"
			}
			if tc.source != "" {
				kind := domain.OperatorKindSystem
				if tc.endType == domain.AlertEndTypeUser {
					kind = domain.OperatorKindUser
				}
				alert.EndOperation = &domain.AlertEndOperation{ID: "operation", Source: tc.source, OperatorKind: kind, OperatorID: "alice"}
			}
			client := &Client{level: func(level string) (string, error) { return level, nil }}
			for _, create := range []bool{false, true} {
				fields, _, err := client.fields(t.Context(), alert, create)
				if err != nil {
					t.Fatal(err)
				}
				if fields["action"] != tc.wantAction || fields["source_alarm_status"] != tc.wantSource {
					t.Fatalf("create=%v: action=%v source=%v, want %s/%s", create, fields["action"], fields["source_alarm_status"], tc.wantAction, tc.wantSource)
				}
			}
			if tc.status != domain.AlertStatusClosed {
				return
			}
			requests := 0
			client = newClient(config.KACPluginConfig{AlarmEventIndex: "alarm"}, closeTransportFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				if req.URL.Path != "/_bulk" || req.URL.Query().Get("refresh") != "false" {
					t.Fatal(req.URL)
				}
				raw, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
				if len(lines) != 2 {
					t.Fatalf("unexpected bulk body: %s", raw)
				}
				var body struct {
					Doc map[string]any `json:"doc"`
				}
				if err := json.Unmarshal([]byte(lines[1]), &body); err != nil {
					t.Fatal(err)
				}
				if len(body.Doc) != 5 || body.Doc["action"] != "close" || body.Doc["source_alarm_status"] != tc.wantSource || body.Doc["close_reason"] != "maintenance" || body.Doc["close_time"] != alert.EndAt.In(kacZone).Format(kacTimeLayout) {
					t.Fatalf("incorrect terminal patch: %v", body.Doc)
				}
				if tc.source == "manual" && body.Doc["status"] != "closed" {
					t.Fatalf("manual disposal did not close: %v", body.Doc)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"items":[{"update":{"status":200,"_index":"alarm-1"}}]}`))}, nil
			}), nil, nil)
			prepareClientForWriteTest(t, client)
			client.locations[alarmKey{tenant: alert.BKTenantID, alert: alert.AlertID}] = "alarm-1"
			results := client.WriteCloseBatch(t.Context(), []domain.Alert{alert})
			if requests != 1 || len(results) != 1 || results[0].Err != nil {
				t.Fatal(requests, results)
			}
		})
	}
}
