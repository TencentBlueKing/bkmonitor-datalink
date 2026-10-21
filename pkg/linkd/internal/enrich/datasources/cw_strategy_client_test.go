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
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"strings"
	"testing"
)

func TestCWStrategyClientReadsHistoricalRuntimeConfig(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name            string
		change          func(map[string]any)
		business        int64
		override, cloud bool
	}{
		{name: "default", business: 2},
		{name: "non-BKCC historical space", business: -255, change: func(p map[string]any) {
			revisionStrategy(p)["bk_biz_id"] = int64(-255)
			revisionStrategy(p)["space_uid"] = "bkci__blueking"
			revisionLabels(p)["bk_biz_id"] = int64(-255)
		}},
		{name: "global business from snapshot", business: 5, change: func(p map[string]any) { revisionStrategy(p)["bk_biz_id"] = int64(5) }},
		{name: "override instance", business: 2, override: true, change: func(p map[string]any) {
			revisionLabels(p)["is_default"] = false
			revisionLabels(p)["bk_object_inst_id"] = 101
		}},
		{name: "cloud", business: 2, cloud: true, change: func(p map[string]any) { revisionSpec(p)["cloud_type"] = "aws" }},
		{name: "disabled historical snapshot remains readable", business: 2, change: func(p map[string]any) { revisionSpec(p)["enable"] = false }},
		{name: "unused runtime DTO", business: 2, change: func(p map[string]any) {
			revisionItem(p)["query_configs"].([]any)[0].(map[string]any)["index_set_id"] = ""
		}},
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
				assertRevisionQuery(t, ctx, statement, args)
				p := revisionTestSnapshot(t)
				if tc.change != nil {
					tc.change(p)
				}
				return snapshotTestRows(t, p), nil
			})
			client, err := NewCWStrategyClient(CWStrategyClientConfig{DB: db})
			if err != nil {
				t.Fatal(err)
			}
			result, found, err := client.GetByStrategyID(t.Context(), models.StrategyRevisionQuery{TenantID: "tenant", ID: 1, Version: revisionTestVersion})
			if err != nil || !found || result.Status.BKStrategyID != 1 || result.MonitorTemplateName != "CPU template" || result.Spec.Name != "CPU" || calls != 2 {
				t.Fatalf("result=%+v found=%v error=%v calls=%d", result, found, err, calls)
			}
			if result.BKBizID == nil || *result.BKBizID != tc.business || *result.IsDefault == tc.override {
				t.Fatal("historical identity lost")
			}
			if tc.business < 0 && result.SpaceUID != "bkci__blueking" {
				t.Fatal("historical source space lost")
			}
			if tc.override && (result.BKObjectInstID == nil || *result.BKObjectInstID != "101") {
				t.Fatal("override instance lost")
			}
			if tc.cloud && result.Kind != models.CWStrategyKindCloud {
				t.Fatal("cloud kind lost")
			}
			projection, err := result.StrategyItemProjection()
			if err != nil || projection.QueryConfigs[0].MetricField != "usage" {
				t.Fatalf("projection=%+v error=%v", projection, err)
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
		{name: "version mismatch", wantInvalid: true, change: func(r *readerTestRows) { r.values[0][3] = revisionTestVersion + 1 }},
		{name: "malformed publication", wantInvalid: true, change: func(r *readerTestRows) { r.values[0][7] = []byte("{") }},
		{name: "oversized publication excluded by SQL", wantInvalid: true, change: func(r *readerTestRows) { r.values[0][7] = nil }},
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
				rows := snapshotTestRows(t, revisionTestSnapshot(t))
				tc.change(rows)
				return rows, nil
			})
			client, err := NewCWStrategyClient(CWStrategyClientConfig{DB: db})
			if err != nil {
				t.Fatal(err)
			}
			_, found, err := client.GetByStrategyID(t.Context(), models.StrategyRevisionQuery{TenantID: "tenant", ID: 1, Version: revisionTestVersion})
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
	identity := models.StrategyRevisionQuery{TenantID: "tenant", ID: 1, Version: revisionTestVersion}
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
