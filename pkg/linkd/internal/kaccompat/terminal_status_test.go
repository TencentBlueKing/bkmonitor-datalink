// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package kaccompat

import (
	"testing"

	"linkd/internal/domain"
	"linkd/internal/store/storetest"
)

func TestTerminalProjectionPreservesCloseSource(t *testing.T) {
	client := &Client{level: func(level string) (string, error) { return level, nil }}
	cases := []struct {
		name    string
		status  domain.AlertStatus
		endType domain.AlertEndType
		source  string
		want    string
	}{
		{"auto policy", domain.AlertStatusClosed, domain.AlertEndTypeSystem, "auto_policy", "auto_closed"},
		{"self heal", domain.AlertStatusClosed, domain.AlertEndTypeSystem, "self_heal", "autoexecute_closed"},
		{"work order system", domain.AlertStatusClosed, domain.AlertEndTypeSystem, "work_order", "autoorder_closed"},
		{"work order user", domain.AlertStatusClosed, domain.AlertEndTypeUser, "work_order", "autoorder_closed"},
		{"manual", domain.AlertStatusClosed, domain.AlertEndTypeUser, "manual", "closed"},
		{"strategy change", domain.AlertStatusClosed, domain.AlertEndTypeSystem, "strategy_change", "system_closed"},
		{"system", domain.AlertStatusClosed, domain.AlertEndTypeSystem, "system", "system_closed"},
		{"legacy user", domain.AlertStatusClosed, domain.AlertEndTypeUser, "", "closed"},
		{"legacy system", domain.AlertStatusClosed, domain.AlertEndTypeSystem, "", "system_closed"},
		{"source event", domain.AlertStatusClosed, domain.AlertEndTypeSource, "", "source_closed"},
		{"source severity upgrade", domain.AlertStatusClosed, domain.AlertEndTypeSeverityUpgrade, "", "source_closed"},
		{"recovery", domain.AlertStatusRecovered, domain.AlertEndTypeSource, "", "restored"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			alert := storetest.Alert("tenant", "alert", "event", "fp", "warning")
			alert.Status, alert.EndType, alert.EndAt = tc.status, tc.endType, &alert.UpdateAt
			if tc.source != "" {
				operatorKind := domain.OperatorKindSystem
				if tc.endType == domain.AlertEndTypeUser {
					operatorKind = domain.OperatorKindUser
				}
				alert.EndOperation = &domain.AlertEndOperation{ID: "close-command", Source: tc.source, OperatorKind: operatorKind, OperatorID: "operator"}
			}
			for _, create := range []bool{false, true} {
				_, mode, err := client.fields(t.Context(), alert, create)
				if err != nil {
					t.Fatal(err)
				}
				if mode != tc.want {
					t.Fatalf("create=%v: mode=%q, want %q", create, mode, tc.want)
				}
			}
		})
	}
}
