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

	"linkd/internal/enrich"
)

func TestAPMApplicationClientMatchesStoredApplicationNames(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		queryName   string
		rows        [][]driver.Value
		wantCount   int
		wantInvalid bool
	}{
		{name: "namespace", queryName: "cross-bank-trade", rows: [][]driver.Value{{"system", int64(49), "cross-bank-trade", "cross_bank_trade", "跨应用-银行交易系统", int64(2)}}, wantCount: 1},
		{name: "normalized name from alarmd", queryName: "cross_bank_trade", rows: [][]driver.Value{{"system", int64(49), "cross-bank-trade", "cross_bank_trade", "跨应用-银行交易系统", int64(2)}}, wantCount: 1},
		{name: "legacy empty normalized name", queryName: "demo", rows: [][]driver.Value{{"system", int64(49), "demo", "", "Demo", int64(2)}}, wantCount: 1},
		{name: "not found", queryName: "missing"},
		{name: "different tenant", queryName: "cross_bank_trade", rows: [][]driver.Value{{"other", int64(49), "cross-bank-trade", "cross_bank_trade", "Other", int64(2)}}, wantInvalid: true},
		{name: "different business", queryName: "cross_bank_trade", rows: [][]driver.Value{{"system", int64(49), "cross-bank-trade", "cross_bank_trade", "Other", int64(3)}}, wantInvalid: true},
		{name: "nonexact normalized name", queryName: "CROSS_BANK_TRADE", rows: [][]driver.Value{{"system", int64(49), "cross-bank-trade", "cross_bank_trade", "Other", int64(2)}}, wantInvalid: true},
		{name: "unrelated application", queryName: "missing", rows: [][]driver.Value{{"system", int64(49), "cross-bank-trade", "cross_bank_trade", "Other", int64(2)}}, wantInvalid: true},
		{name: "invalid application ID", queryName: "cross_bank_trade", rows: [][]driver.Value{{"system", int64(0), "cross-bank-trade", "cross_bank_trade", "Other", int64(2)}}, wantInvalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			database := openReaderTestDB(t, func(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
				if !strings.Contains(query, "bk_tenant_id = ? AND bk_biz_id = ? AND (namespace = ? OR normalized_name = ?) AND is_deleted = ?") {
					t.Fatalf("application names must stay inside tenant/business scope: %s", query)
				}
				want := []driver.Value{"system", int64(2), test.queryName, test.queryName, false}
				if len(args) != len(want) {
					t.Fatalf("args=%v", args)
				}
				for i := range want {
					if args[i].Value != want[i] {
						t.Fatalf("args[%d]=%v want=%v", i, args[i].Value, want[i])
					}
				}
				return &readerTestRows{columns: []string{"bk_tenant_id", "namespace_id", "namespace", "normalized_name", "namespace_alias", "bk_biz_id"}, values: test.rows}, nil
			})
			client, err := NewAPMApplicationClient(APMApplicationClientConfig{DB: database})
			if err != nil {
				t.Fatal(err)
			}
			apps, err := client.FindAPMApplications(context.Background(), "system", 2, test.queryName)
			if test.wantInvalid {
				if !errors.Is(err, enrich.ErrInvalidDataSourceResponse) || len(apps) != 0 {
					t.Fatalf("apps=%#v err=%v", apps, err)
				}
				return
			}
			if err != nil || len(apps) != test.wantCount {
				t.Fatalf("apps=%#v err=%v", apps, err)
			}
			if len(apps) > 0 && (apps[0].Name != test.queryName || apps[0].ID != 49 || apps[0].Alias == "") {
				t.Fatalf("matched query name and application identity must be preserved: %#v", apps)
			}
		})
	}
}

func TestAPMApplicationClientScopesQueryByTenantBusinessAndName(t *testing.T) {
	t.Parallel()
	database := openReaderTestDB(t, func(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
		for _, fragment := range []string{"`kapm_namespace`", "bk_tenant_id = ?", "bk_biz_id = ?", "namespace = ?", "is_deleted = ?"} {
			if !strings.Contains(query, fragment) {
				t.Fatalf("query %q does not contain %q", query, fragment)
			}
		}
		want := []driver.Value{"system", int64(10), "test223", "test223", false}
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

func TestAPMApplicationClientPreservesQueryError(t *testing.T) {
	t.Parallel()
	for _, queryErr := range []error{errors.New("database unavailable"), context.Canceled} {
		t.Run(queryErr.Error(), func(t *testing.T) {
			database := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
				return nil, queryErr
			})
			client, err := NewAPMApplicationClient(APMApplicationClientConfig{DB: database})
			if err != nil {
				t.Fatal(err)
			}
			if apps, err := client.FindAPMApplications(context.Background(), "system", 2, "cross_bank_trade"); !errors.Is(err, queryErr) || len(apps) != 0 {
				t.Fatalf("apps=%#v err=%v", apps, err)
			}
		})
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
