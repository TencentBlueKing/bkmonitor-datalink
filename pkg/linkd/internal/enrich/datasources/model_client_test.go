// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package datasources

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"linkd/internal/enrich"
)

func TestModelClientGetModelByCode(t *testing.T) {
	t.Parallel()
	database := openReaderTestDB(t, func(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, fragment := range []string{"object_model_v2", "bk_tenant_id", "model_id", "display_fields"} {
			if !strings.Contains(query, fragment) {
				t.Fatalf("query=%s missing %q", query, fragment)
			}
		}
		got := make([]any, len(args))
		for index := range args {
			got[index] = args[index].Value
		}
		if len(got) != 3 || got[0] != "tenant-a" || got[1] != "cw-Service" {
			t.Fatalf("args=%#v", got)
		}
		return &readerTestRows{
			columns: []string{"object_model_id", "bk_tenant_id", "model_id", "model_name", "bk_cmdb_obj_id", "display_fields", "inst_display_name", "host_related_field"},
			values:  [][]driver.Value{{int64(20), "tenant-a", "cw-Service", "服务", "service_instance", `[{"bk_property_id":"name"}]`, "${name}", "service_run_host"}},
		}, nil
	})
	client, err := NewModelClient(ModelClientConfig{DB: database})
	if err != nil {
		t.Fatal(err)
	}
	model, found, err := client.GetModelByCode(context.Background(), "tenant-a", "cw-Service")
	if err != nil || !found || model.ModelID != "20" || model.Fields["object_model_name"] != "服务" ||
		model.Fields["host_related_field"] != "service_run_host" {
		t.Fatalf("GetModelByCode()=%#v,%t,%v", model, found, err)
	}
}

func TestModelClientMissingAndInvalidResponse(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		rows    driver.Rows
		found   bool
		invalid bool
	}{
		{name: "missing", rows: &readerTestRows{columns: []string{"object_model_id", "bk_tenant_id", "model_id", "model_name", "bk_cmdb_obj_id", "display_fields", "inst_display_name", "host_related_field"}}},
		{
			name: "tenant mismatch",
			rows: &readerTestRows{
				columns: []string{"object_model_id", "bk_tenant_id", "model_id", "model_name", "bk_cmdb_obj_id", "display_fields", "inst_display_name", "host_related_field"},
				values:  [][]driver.Value{{int64(20), "tenant-b", "cw-Service", "服务", "service_instance", `[]`, "", ""}},
			},
			invalid: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			database := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) { return test.rows, nil })
			client, _ := NewModelClient(ModelClientConfig{DB: database})
			_, found, err := client.GetModelByCode(context.Background(), "tenant-a", "cw-Service")
			if found != test.found || test.invalid != errors.Is(err, enrich.ErrInvalidDataSourceResponse) {
				t.Fatalf("found=%t err=%v", found, err)
			}
		})
	}
}

func TestModelClientPropagatesCancellation(t *testing.T) {
	database := openReaderTestDB(t, func(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	client, _ := NewModelClient(ModelClientConfig{DB: database})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := client.GetModelByCode(ctx, "tenant-a", "cw-Service")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}
