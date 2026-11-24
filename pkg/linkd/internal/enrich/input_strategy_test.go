// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich

import (
	"testing"

	"linkd/internal/domain"
)

func TestValidateStrategyIdentityIgnoresBusinessAndKeepsStableIdentity(t *testing.T) {
	t.Parallel()
	negative, err := domain.NewNumberScalar(-255)
	if err != nil {
		t.Fatal(err)
	}
	zero, err := domain.NewNumberScalar(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*domain.Event)
		field  string
		code   DiagnosticCode
	}{
		{name: "positive business"},
		{name: "missing business", change: func(e *domain.Event) { delete(e.Labels, labelBizID) }},
		{name: "non-BKCC space", change: func(e *domain.Event) { e.Labels[labelBizID] = negative }},
		{name: "zero business", change: func(e *domain.Event) { e.Labels[labelBizID] = zero }},
		{name: "unusable business", change: func(e *domain.Event) { e.Labels[labelBizID] = domain.NewStringScalar("invalid") }},
		{name: "missing tenant", change: func(e *domain.Event) { e.BKTenantID = "" }, field: "bk_tenant_id", code: DiagnosticCodeMissingField},
		{name: "invalid tenant", change: func(e *domain.Event) { e.BKTenantID = " tenant-1 " }, field: "bk_tenant_id", code: DiagnosticCodeInvalidField},
		{name: "missing strategy", change: func(e *domain.Event) { delete(e.Labels, labelStrategyID) }, field: "labels.strategy_id", code: DiagnosticCodeMissingField},
		{name: "missing revision", change: func(e *domain.Event) { delete(e.Labels, labelStrategyVersion) }, field: "labels.strategy_version", code: DiagnosticCodeMissingField},
		{name: "zero strategy", change: func(e *domain.Event) { e.Labels[labelStrategyID] = zero }, field: "labels.strategy_id", code: DiagnosticCodeInvalidField},
		{name: "negative revision", change: func(e *domain.Event) { e.Labels[labelStrategyVersion] = negative }, field: "labels.strategy_version", code: DiagnosticCodeInvalidField},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := testAlert()
			if tc.change != nil {
				tc.change(&event)
			}
			identity, diagnostics := ValidateStrategyIdentity(event)
			if tc.field != "" {
				if len(diagnostics) != 1 || diagnostics[0].Code != tc.code || len(diagnostics[0].Fields) != 1 || diagnostics[0].Fields[0] != tc.field {
					t.Fatalf("diagnostics=%+v", diagnostics)
				}
			} else if len(diagnostics) != 0 || !identity.Valid() || identity.TenantID != "tenant-1" || identity.ID != 123 || identity.Version != 1 {
				t.Fatalf("identity=%+v diagnostics=%+v", identity, diagnostics)
			}
		})
	}
}

func TestResourceIdentityStillRequiresPositiveBusiness(t *testing.T) {
	t.Parallel()
	for _, missing := range []bool{true, false} {
		event := testAlert()
		if missing {
			delete(event.Labels, labelBizID)
		} else {
			value, err := domain.NewNumberScalar(-255)
			if err != nil {
				t.Fatal(err)
			}
			event.Labels[labelBizID] = value
		}
		if _, diagnostics := ValidateRequiredIDs(event); len(diagnostics) != 1 || len(diagnostics[0].Fields) != 1 || diagnostics[0].Fields[0] != "labels.bk_biz_id" {
			t.Fatalf("resource business validation changed: %+v", diagnostics)
		}
	}
}
