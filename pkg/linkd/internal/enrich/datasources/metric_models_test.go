// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package datasources

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
)

func TestMetricClientAuthoritativeModels(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		models    []string
		filter    string
		badTenant bool
		badMetric bool
		readErr   bool
		wantErr   bool
	}{
		{name: "unassociated despite stale scalar", models: []string{}},
		{name: "one model", models: []string{"cw-Host"}},
		{name: "multiple models with null scalar", models: []string{"cw-Host", "cw-MySQL"}},
		{name: "reverse lookup selects member", models: []string{"cw-Host", "cw-MySQL"}, filter: "cw-MySQL"},
		{name: "reverse lookup rejects nonmember", models: []string{"cw-Host"}, filter: "cw-MySQL", wantErr: true},
		{name: "foreign relation", models: []string{"cw-Host"}, badTenant: true, wantErr: true},
		{name: "wrong metric relation", models: []string{"cw-Host"}, badMetric: true, wantErr: true},
		{name: "duplicate relation", models: []string{"cw-Host", "cw-Host"}, wantErr: true},
		{name: "relation dependency failure", readErr: true, wantErr: true},
		{name: "invalid model", models: []string{""}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var relationRead bool
			db := openReaderTestDB(t, func(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
				if strings.Contains(query, "FROM `metric_object`") {
					relationRead = true
					if tc.readErr {
						return nil, errors.New("relation unavailable")
					}
					rows := make([][]driver.Value, 0, len(tc.models))
					for _, model := range tc.models {
						tenant := "tenant-a"
						if tc.badTenant {
							tenant = "tenant-b"
						}
						metricID := int64(42)
						if tc.badMetric {
							metricID = 43
						}
						rows = append(rows, []driver.Value{tenant, metricID, model})
					}
					return &readerTestRows{columns: []string{"bk_tenant_id", "metric_id", "model_id"}, values: rows}, nil
				}
				if tc.filter != "" && !strings.Contains(query, "EXISTS") {
					t.Error("reverse lookup must filter authoritative model membership")
				}
				row := metricTestRow()
				if len(tc.models) > 1 {
					row[3] = nil
				}
				columns := append(metricTestColumns(), "ref_metricset_code", "record_rule_id")
				row = append(row, "plugin.cpu", int64(7))
				return &readerTestRows{columns: columns, values: [][]driver.Value{row}}, nil
			})
			client, _ := NewMetricClient(MetricClientConfig{DB: db})
			query := models.MetricQuery{TenantID: "tenant-a", MetricID: 42}
			if tc.filter != "" {
				query = models.MetricQuery{TenantID: "tenant-a", TableID: "system.cpu", FieldName: "usage", ObjectModelCode: tc.filter}
			}
			got, found, err := client.FindMetric(t.Context(), query)
			if (err != nil) != tc.wantErr || found == tc.wantErr {
				t.Fatalf("found=%v err=%v", found, err)
			}
			if tc.wantErr {
				return
			}
			if !relationRead || !reflect.DeepEqual(got.ModelIDs, tc.models) || got.RefMetricsetCode != "plugin.cpu" || got.RecordRuleID == nil || *got.RecordRuleID != 7 {
				t.Fatalf("projection=%+v relationRead=%v", got, relationRead)
			}
			wantScalar := ""
			if len(tc.models) == 1 {
				wantScalar = tc.models[0]
			}
			if got.ObjectModelCode != wantScalar {
				t.Fatalf("derived model=%q want=%q", got.ObjectModelCode, wantScalar)
			}
		})
	}
}

func TestMetricClientRelationBudget(t *testing.T) {
	db := openReaderTestDB(t, func(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
		if strings.Contains(query, "FROM `metric_object`") {
			rows := make([][]driver.Value, 8193)
			for i := range rows {
				rows[i] = []driver.Value{"tenant-a", int64(42), "cw-Host"}
			}
			return &readerTestRows{columns: []string{"bk_tenant_id", "metric_id", "model_id"}, values: rows}, nil
		}
		return &readerTestRows{columns: metricTestColumns(), values: [][]driver.Value{metricTestRow()}}, nil
	})
	client, _ := NewMetricClient(MetricClientConfig{DB: db})
	_, found, err := client.FindMetric(t.Context(), models.MetricQuery{TenantID: "tenant-a", MetricID: 42})
	if found || !errors.Is(err, enrich.ErrInvalidDataSourceResponse) {
		t.Fatalf("found=%v err=%v", found, err)
	}
}
