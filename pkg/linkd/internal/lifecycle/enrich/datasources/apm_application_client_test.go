// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 蓝鲸监控 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package datasources

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"linkd/internal/lifecycle/enrich"
)

func TestAPMApplicationClientScopesQueryByTenantBusinessAndName(t *testing.T) {
	t.Parallel()
	database := openReaderTestDB(t, func(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
		for _, fragment := range []string{"`kapm_namespace`", "bk_tenant_id = ?", "bk_biz_id = ?", "namespace = ?", "is_deleted = ?"} {
			if !strings.Contains(query, fragment) {
				t.Fatalf("query %q does not contain %q", query, fragment)
			}
		}
		want := []driver.Value{"system", int64(10), "test223", false}
		if len(args) != len(want) {
			t.Fatalf("args=%v", args)
		}
		for index := range want {
			if args[index].Value != want[index] {
				t.Fatalf("args[%d]=%v want=%v", index, args[index].Value, want[index])
			}
		}
		return &readerTestRows{
			columns: []string{"bk_tenant_id", "namespace_id", "namespace", "namespace_alias", "bk_biz_id"},
			values:  [][]driver.Value{{"system", int64(29), "test223", "Test 223", int64(10)}},
		}, nil
	})
	client, err := NewAPMApplicationClient(APMApplicationClientConfig{DB: database})
	if err != nil {
		t.Fatal(err)
	}
	apps, err := client.FindAPMApplications(context.Background(), "system", 10, "test223")
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].TenantID != "system" || apps[0].BKBizID != 10 || apps[0].ID != 29 || apps[0].Name != "test223" || apps[0].Alias != "Test 223" {
		t.Fatalf("apps=%#v", apps)
	}
}

func TestAPMApplicationClientRejectsInvalidRequestsWithoutQuery(t *testing.T) {
	t.Parallel()
	database := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		t.Fatal("unexpected database query")
		return nil, nil
	})
	client, err := NewAPMApplicationClient(APMApplicationClientConfig{DB: database})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		ctx      context.Context
		tenantID string
		bizID    int64
		appName  string
	}{
		{name: "nil context", tenantID: "system", bizID: 10, appName: "test223"},
		{name: "empty tenant", ctx: context.Background(), bizID: 10, appName: "test223"},
		{name: "zero business", ctx: context.Background(), tenantID: "system", appName: "test223"},
		{name: "empty name", ctx: context.Background(), tenantID: "system", bizID: 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := client.FindAPMApplications(test.ctx, test.tenantID, test.bizID, test.appName); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
}

func TestAPMApplicationClientRejectsMismatchedResponseIdentity(t *testing.T) {
	t.Parallel()
	database := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		return &readerTestRows{
			columns: []string{"bk_tenant_id", "namespace_id", "namespace", "namespace_alias", "bk_biz_id"},
			values:  [][]driver.Value{{"other", int64(29), "test223", "Test 223", int64(10)}},
		}, nil
	})
	client, err := NewAPMApplicationClient(APMApplicationClientConfig{DB: database})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.FindAPMApplications(context.Background(), "system", 10, "test223"); !errors.Is(err, enrich.ErrInvalidDataSourceResponse) {
		t.Fatalf("err=%v", err)
	}
}
