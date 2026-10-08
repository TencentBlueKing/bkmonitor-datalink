// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasources

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
)

func TestCWStrategyClientReadsPublication(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name            string
		businesses      []int64
		override, cloud bool
	}{
		{name: "default without old tables", businesses: []int64{2}},
		{name: "data multiple businesses", businesses: []int64{2, 4}},
		{name: "override uses child identity", businesses: []int64{2}, override: true},
		{name: "cloud fields in publication", businesses: []int64{2}, cloud: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			db := openReaderTestDB(t, func(ctx context.Context, statement string, args []driver.NamedValue) (driver.Rows, error) {
				calls++
				if strings.Contains(statement, "home_application_monitortemplate") {
					if args[0].Value != int64(7) || args[1].Value != "tenant" {
						t.Fatal("template not tenant scoped")
					}
					return &readerTestRows{columns: []string{"id", "bk_tenant_id", "name"}, values: [][]driver.Value{{int64(7), "tenant", "CPU template"}}}, nil
				}
				assertSplitQuery(t, ctx, statement, args)
				payload := splitTestPublication(t, tc.businesses...)
				if tc.cloud {
					payload["resolved_strategies"].([]any)[0].(map[string]any)["spec"].(map[string]any)["cloud_type"] = "aws"
				}
				if tc.override {
					splitTestLabels(payload)["is_default"] = false
					splitTestLabels(payload)["bk_object_inst_id"] = 101
					delete(payload, "strategy_set")
					delete(payload, "strategy_config")
					delete(payload, "runtime_query_configs")
				}
				rows := splitTestRows(t, payload)
				if tc.override {
					rows.values[0][1] = int64(9)
				}
				return rows, nil
			})
			client, err := NewCWStrategyClient(CWStrategyClientConfig{DB: db})
			if err != nil {
				t.Fatal(err)
			}
			result, found, err := client.GetByStrategyID(t.Context(), models.StrategyQuery{TenantID: "tenant", ID: 1, Version: splitTestVersion})
			if err != nil || !found || result.Status.BKStrategyID != 1 || result.MonitorTemplateName != "CPU template" || result.Spec.Name != "CPU" || calls != 2 {
				t.Fatalf("found=%t err=%v result=%+v calls=%d", found, err, result, calls)
			}
			if *result.IsDefault == tc.override {
				t.Fatal("default/override identity lost")
			}
			if tc.override && (result.BKObjectInstID == nil || *result.BKObjectInstID != "101") {
				t.Fatal("numeric override instance identity lost")
			}
			if tc.cloud && result.Kind != models.CWStrategyKindCloud {
				t.Fatal("cloud kind lost")
			}
			projection, err := result.StrategyItemProjection()
			if err != nil || projection.QueryConfigs[0].MetricField != "usage" {
				t.Fatalf("projection=%+v err=%v", projection, err)
			}
		})
	}
}

func TestCWStrategyClientMissingInvalidFailureAndCancellation(t *testing.T) {
	t.Parallel()
	dependency := errors.New("database unavailable")
	for _, tc := range []struct {
		name        string
		failure     error
		change      func(*readerTestRows)
		wantInvalid bool
	}{
		{name: "missing"},
		{name: "dependency", failure: dependency},
		{name: "version mismatch", wantInvalid: true, change: func(r *readerTestRows) { r.values[0][6] = splitTestVersion + 1 }},
		{name: "malformed publication", wantInvalid: true, change: func(r *readerTestRows) { r.values[0][11] = "{" }},
		{name: "oversized publication excluded by SQL", wantInvalid: true, change: func(r *readerTestRows) { r.values[0][11] = nil }},
		{name: "duplicate rows", wantInvalid: true, change: func(r *readerTestRows) { r.values = append(r.values, r.values[0]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
				if tc.failure != nil {
					return nil, tc.failure
				}
				if tc.change == nil {
					return &readerTestRows{columns: []string{"id"}}, nil
				}
				rows := splitTestRows(t, splitTestPublication(t, 2))
				tc.change(rows)
				return rows, nil
			})
			client, err := NewCWStrategyClient(CWStrategyClientConfig{DB: db})
			if err != nil {
				t.Fatal(err)
			}
			_, found, err := client.GetByStrategyID(t.Context(), models.StrategyQuery{TenantID: "tenant", ID: 1, Version: splitTestVersion})
			if found {
				t.Fatal("unexpected strategy")
			}
			if tc.failure != nil {
				if !errors.Is(err, dependency) {
					t.Fatal(err)
				}
			} else if tc.wantInvalid {
				if !errors.Is(err, enrich.ErrInvalidDataSourceResponse) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := NewCWStrategyClient(CWStrategyClientConfig{}); err == nil {
		t.Fatal("nil DB accepted")
	}
	db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		t.Fatal("invalid request reached SQL")
		return nil, nil
	})
	client, err := NewCWStrategyClient(CWStrategyClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	identity := models.StrategyQuery{TenantID: "tenant", ID: 1, Version: splitTestVersion}
	if _, _, err := client.GetByStrategyID(ctx, identity); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err := client.GetByStrategyID(nil, identity); err == nil { //nolint:staticcheck // SA1012: 验证调用方误传 nil Context 时明确拒绝，且不执行 SQL。
		t.Fatal("nil context accepted")
	}
	identity.Version = 0
	if _, _, err := client.GetByStrategyID(t.Context(), identity); err == nil {
		t.Fatal("missing version accepted")
	}
}
