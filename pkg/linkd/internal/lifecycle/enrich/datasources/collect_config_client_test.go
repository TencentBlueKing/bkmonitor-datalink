// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package datasources

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
)

func TestNewCollectConfigClientValidatesConfig(t *testing.T) {
	t.Parallel()
	client, err := NewCollectConfigClient(CollectConfigClientConfig{})
	if err == nil || client != nil {
		t.Fatalf("NewCollectConfigClient()=%v,%v, want nil,error", client, err)
	}
}

func TestCollectConfigClientGetCollectConfigUsesSchemaFields(t *testing.T) {
	t.Parallel()
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "query-context")
	var queryCount int
	db := openReaderTestDB(t, func(queryCtx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
		queryCount++
		if queryCtx.Value(contextKey{}) != "query-context" {
			t.Error("request context was not propagated to SQL")
		}
		query = strings.Join(strings.Fields(strings.ReplaceAll(query, "`", "")), " ")
		if !strings.HasPrefix(query, "SELECT uid, bk_tenant_id, bk_object_code, bk_inst_id, final_biz_id, CASE JSON_UNQUOTE(JSON_EXTRACT(spec, '$.remote_collect_info.is_remote_collect')) WHEN 'true' THEN 1 ELSE 0 END AS remote_collect, JSON_UNQUOTE(JSON_EXTRACT(status, '$.bk_collect_task_id')) AS bk_collect_task_id, JSON_TYPE(JSON_EXTRACT(status, '$.bk_collect_task_id')) AS task_id_type FROM core_v1alpha1_collect ") ||
			!strings.Contains(query, "bk_tenant_id = ? AND active = ?") ||
			!strings.Contains(query, "JSON_UNQUOTE(JSON_EXTRACT(status, '$.bk_collect_task_id')) = ?") ||
			!strings.Contains(query, "delete_status") || !strings.Contains(query, "LIMIT ") {
			t.Errorf("unexpected query: %s", query)
		}
		if len(args) < 5 || args[0].Value != "tenant-a" || args[1].Value != true || args[2].Value != "0017" ||
			args[3].Value != "deleting" || args[4].Value != "deleted" {
			t.Errorf("query args=%#v", args)
		}
		return &readerTestRows{
			columns: []string{"uid", "bk_tenant_id", "bk_object_code", "bk_inst_id", "final_biz_id", "remote_collect", "bk_collect_task_id", "task_id_type"},
			values:  [][]driver.Value{{"8a198069-e040-43ea-ab9e-b383668bdc6a", "tenant-a", "cw-Service", int64(23), int64(2), int64(1), "0017", "STRING"}},
		}, nil
	})
	client, err := NewCollectConfigClient(CollectConfigClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := client.GetCollectConfig(ctx, "tenant-a", "0017")
	want := models.CollectConfig{
		UID: "8a198069-e040-43ea-ab9e-b383668bdc6a", BKTenantID: "tenant-a",
		BKCollectTaskID: "0017", BKObjectCode: "cw-Service", BKInstID: 23,
		FinalBizID: int64Pointer(2), IsRemoteCollect: true,
	}
	if err != nil || !found || got.UID != want.UID || got.BKTenantID != want.BKTenantID ||
		got.BKCollectTaskID != want.BKCollectTaskID || got.BKObjectCode != want.BKObjectCode ||
		got.BKInstID != want.BKInstID || got.FinalBizID == nil || *got.FinalBizID != 2 || !got.IsRemoteCollect || queryCount != 1 {
		t.Fatalf("GetCollectConfig()=%#v,%v,%v queries=%d, want %#v,true,nil once", got, found, err, queryCount, want)
	}
}

func TestCollectConfigClientAcceptsNumericJSONTaskID(t *testing.T) {
	t.Parallel()
	db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		return &readerTestRows{
			columns: []string{"uid", "bk_tenant_id", "bk_object_code", "bk_inst_id", "final_biz_id", "remote_collect", "bk_collect_task_id", "task_id_type"},
			values:  [][]driver.Value{{"8a198069-e040-43ea-ab9e-b383668bdc6a", "tenant-a", "cw-Host", int64(7), nil, int64(0), "17", "INTEGER"}},
		}, nil
	})
	client, err := NewCollectConfigClient(CollectConfigClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := client.GetCollectConfig(context.Background(), "tenant-a", "17")
	if err != nil || !found || got.BKCollectTaskID != "17" || got.BKInstID != 7 {
		t.Fatalf("GetCollectConfig()=%#v,%v,%v", got, found, err)
	}
}

func TestCollectConfigClientMissingAndFailures(t *testing.T) {
	t.Parallel()
	databaseErr := errors.New("database unavailable")
	valid := []driver.Value{
		"8a198069-e040-43ea-ab9e-b383668bdc6a", "tenant-a", "cw-Host", int64(7), nil, int64(0), "17", "STRING",
	}
	for _, test := range []struct {
		name     string
		values   [][]driver.Value
		queryErr error
		wantErr  error
	}{
		{name: "missing"},
		{name: "database failure", queryErr: databaseErr, wantErr: databaseErr},
		{name: "duplicate task identity", values: [][]driver.Value{valid, valid}, wantErr: enrich.ErrInvalidDataSourceResponse},
		{name: "missing uid", values: [][]driver.Value{{"", "tenant-a", "cw-Host", int64(7), nil, int64(0), "17", "STRING"}}, wantErr: enrich.ErrInvalidDataSourceResponse},
		{name: "tenant mismatch", values: [][]driver.Value{{"uid", "tenant-b", "cw-Host", int64(7), nil, int64(0), "17", "STRING"}}, wantErr: enrich.ErrInvalidDataSourceResponse},
		{name: "task mismatch", values: [][]driver.Value{{"uid", "tenant-a", "cw-Host", int64(7), nil, int64(0), "18", "STRING"}}, wantErr: enrich.ErrInvalidDataSourceResponse},
		{name: "invalid json type", values: [][]driver.Value{{"uid", "tenant-a", "cw-Host", int64(7), nil, int64(0), "17", "OBJECT"}}, wantErr: enrich.ErrInvalidDataSourceResponse},
		{name: "missing object code", values: [][]driver.Value{{"uid", "tenant-a", nil, int64(7), nil, int64(0), "17", "STRING"}}, wantErr: enrich.ErrInvalidDataSourceResponse},
		{name: "zero instance", values: [][]driver.Value{{"uid", "tenant-a", "cw-Host", int64(0), nil, int64(0), "17", "STRING"}}, wantErr: enrich.ErrInvalidDataSourceResponse},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
				if test.queryErr != nil {
					return nil, test.queryErr
				}
				return &readerTestRows{
					columns: []string{"uid", "bk_tenant_id", "bk_object_code", "bk_inst_id", "final_biz_id", "remote_collect", "bk_collect_task_id", "task_id_type"},
					values:  test.values,
				}, nil
			})
			client, err := NewCollectConfigClient(CollectConfigClientConfig{DB: db})
			if err != nil {
				t.Fatal(err)
			}
			got, found, err := client.GetCollectConfig(context.Background(), "tenant-a", "17")
			if got != (models.CollectConfig{}) || found || !errors.Is(err, test.wantErr) {
				t.Fatalf("GetCollectConfig()=%#v,%v,%v, want zero,false,%v", got, found, err, test.wantErr)
			}
		})
	}
}

func TestCollectConfigClientRejectsInvalidRequestsWithoutQuery(t *testing.T) {
	t.Parallel()
	var queries atomic.Int64
	db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		queries.Add(1)
		return nil, errors.New("unexpected query")
	})
	client, err := NewCollectConfigClient(CollectConfigClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		ctx    context.Context
		tenant string
		taskID string
		miss   bool
	}{
		{name: "nil context", tenant: "tenant-a", taskID: "17"},
		{name: "empty tenant", ctx: context.Background(), taskID: "17"},
		{name: "blank tenant", ctx: context.Background(), tenant: " \t", taskID: "17"},
		{name: "empty task", ctx: context.Background(), tenant: "tenant-a"},
		{name: "blank task", ctx: context.Background(), tenant: "tenant-a", taskID: " \n"},
		{name: "reserved zero task", ctx: context.Background(), tenant: "tenant-a", taskID: "0", miss: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, found, err := client.GetCollectConfig(test.ctx, test.tenant, test.taskID)
			if got != (models.CollectConfig{}) || found || (test.miss && err != nil) || (!test.miss && err == nil) {
				t.Fatalf("GetCollectConfig()=%#v,%v,%v", got, found, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, found, err := client.GetCollectConfig(ctx, "tenant-a", "17")
	if got != (models.CollectConfig{}) || found || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled GetCollectConfig()=%#v,%v,%v", got, found, err)
	}
	if queries.Load() != 0 {
		t.Fatalf("rejected requests issued %d SQL queries", queries.Load())
	}
}

func int64Pointer(value int64) *int64 { return &value }

func TestCollectConfigClientCancelsInFlightQuery(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	db := openReaderTestDB(t, func(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	client, err := NewCollectConfigClient(CollectConfigClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, _, err := client.GetCollectConfig(ctx, "tenant-a", "17")
		result <- err
	}()
	select {
	case <-started:
		cancel()
	case <-ctx.Done():
		t.Fatal("query did not start")
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("GetCollectConfig() error=%v, want context.Canceled", err)
	}
}
