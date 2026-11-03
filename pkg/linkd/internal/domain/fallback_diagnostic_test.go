// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package domain_test

import (
	"encoding/json"
	"linkd/internal/domain"
	"testing"
)

func TestAlertAcceptsFallbackSucceededDiagnostic(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, status, code string
		valid              bool
	}{
		{"success", "succeeded", "fallback_succeeded", true},
		{"partial with other unresolved failure", "partial", "fallback_succeeded", true},
		{"unknown code remains invalid", "succeeded", "fallback_typo", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := payloadAlert(t)
			a.EnrichStatus = domain.EnrichStatus(tc.status)
			a.Enrich["processors"] = json.RawMessage(`[{"k8s":{"status":"` + tc.status + `","patches":[],"diagnostics":[{"code":"` + tc.code + `","reason_code":"instance_not_found","model_id":"cw-K8s_Container"}]}}]`)
			_, err := a.Normalize()
			if (err == nil) != tc.valid {
				t.Fatalf("validation=%v valid=%v", err, tc.valid)
			}
		})
	}
}
