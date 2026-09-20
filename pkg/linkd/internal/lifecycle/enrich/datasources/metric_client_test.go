// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package datasources

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
)

func TestMetricClientRejectsEmptyFieldName(t *testing.T) {
	t.Parallel()
	db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		return &readerTestRows{
			columns: []string{"field_name", "field_cn_name", "description", "unit", "dimension_list"},
			values:  [][]driver.Value{{"", "CPU 使用率", "CPU usage", "percent", []byte(`[]`)}},
		}, nil
	})
	client, err := NewMetricClient(MetricClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := client.FindMetricLibrary(context.Background(), models.MetricLibraryQuery{
		TenantID: "tenant-a", TableID: "system.cpu", FieldName: "usage",
	})
	if found || !errors.Is(err, enrich.ErrInvalidDataSourceResponse) {
		t.Fatalf("FindMetricLibrary() found=%v error=%v", found, err)
	}
}

func TestMetricClientProjectsFieldName(t *testing.T) {
	t.Parallel()
	db := openReaderTestDB(t, func(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
		query = strings.Join(strings.Fields(strings.ReplaceAll(query, "`", "")), " ")
		if !strings.HasPrefix(query, "SELECT object_model_code,field_name,field_cn_name,description,unit,value_mapping,dimension_list FROM home_application_monitormetriclibrary ") {
			t.Errorf("unexpected query: %s", query)
		}
		if len(args) < 4 || args[0].Value != "tenant-a" || args[1].Value != "usage" || args[2].Value != false || args[3].Value != "system.cpu" {
			t.Errorf("query args=%#v", args)
		}
		return &readerTestRows{
			columns: []string{"object_model_code", "field_name", "field_cn_name", "description", "unit", "dimension_list"},
			values:  [][]driver.Value{{"cw-Others", "usage", "CPU 使用率", "CPU usage", "percent", []byte(`[]`)}},
		}, nil
	})
	client, err := NewMetricClient(MetricClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := client.FindMetricLibrary(context.Background(), models.MetricLibraryQuery{
		TenantID: "tenant-a", TableID: "system.cpu", FieldName: "usage",
	})
	if err != nil || !found || got.ObjectModelCode != "cw-Others" || got.FieldName != "usage" || got.FieldCNName != "CPU 使用率" || got.Unit != "percent" {
		t.Fatalf("FindMetricLibrary()=%#v,%v,%v", got, found, err)
	}
}
