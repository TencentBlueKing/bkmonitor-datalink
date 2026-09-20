// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package datasources

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
)

func TestNewUptimeClientValidatesConfig(t *testing.T) {
	t.Parallel()
	client, err := NewUptimeClient(UptimeClientConfig{})
	if err == nil || client != nil {
		t.Fatalf("NewUptimeClient()=%v,%v, want nil,error", client, err)
	}
}

func TestUptimeClientGetUptimeTaskUsesModelFields(t *testing.T) {
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
		if !strings.HasPrefix(query, "SELECT id,task_id,name,protocol,bk_biz_id,bk_tenant_id FROM home_application_uptimechecktask ") ||
			!strings.Contains(query, "WHERE bk_tenant_id = ? AND task_id = ? AND is_deleted = ?") || !strings.Contains(query, "LIMIT ") {
			t.Errorf("unexpected query: %s", query)
		}
		if len(args) < 3 || args[0].Value != "tenant-a" || args[1].Value != int64(7) || args[2].Value != false {
			t.Errorf("query args=%#v, want tenant-a, task_id=7 and is_deleted=false", args)
		}
		return &readerTestRows{
			columns: []string{"id", "task_id", "name", "protocol", "bk_biz_id", "bk_tenant_id"},
			values:  [][]driver.Value{{int64(42), int64(7), "HTTP 可用性", "HTTP", int64(0), "tenant-a"}},
		}, nil
	})
	client, err := NewUptimeClient(UptimeClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := client.GetUptimeTask(ctx, "tenant-a", "007")
	want := models.UptimeTask{ID: 42, TaskID: 7, Name: "HTTP 可用性", Protocol: models.UptimeProtocolHTTP, BKBizID: 0, BKTenantID: "tenant-a"}
	if err != nil || !found || got != want || queryCount != 1 {
		t.Fatalf("GetUptimeTask()=%#v,%v,%v queries=%d, want %#v,true,nil once", got, found, err, queryCount, want)
	}
}

func TestUptimeClientGetUptimeTaskMissingAndFailures(t *testing.T) {
	t.Parallel()
	databaseErr := errors.New("database unavailable")
	for _, test := range []struct {
		name     string
		values   [][]driver.Value
		queryErr error
		wantErr  error
	}{
		{name: "missing or deleted"},
		{name: "database failure", queryErr: databaseErr, wantErr: databaseErr},
		{name: "mismatched task ID", values: [][]driver.Value{{int64(42), int64(8), "任务", "HTTP", int64(2), "tenant-a"}}, wantErr: enrich.ErrInvalidDataSourceResponse},
		{name: "zero primary key", values: [][]driver.Value{{int64(0), int64(7), "任务", "HTTP", int64(2), "tenant-a"}}, wantErr: enrich.ErrInvalidDataSourceResponse},
		{name: "mismatched tenant", values: [][]driver.Value{{int64(42), int64(7), "任务", "HTTP", int64(2), "tenant-b"}}, wantErr: enrich.ErrInvalidDataSourceResponse},
		{name: "invalid protocol", values: [][]driver.Value{{int64(42), int64(7), "任务", "SMTP", int64(2), "tenant-a"}}, wantErr: enrich.ErrInvalidDataSourceResponse},
		{name: "negative business ID", values: [][]driver.Value{{int64(42), int64(7), "任务", "HTTP", int64(-1), "tenant-a"}}, wantErr: enrich.ErrInvalidDataSourceResponse},
		{name: "duplicate task response", values: [][]driver.Value{
			{int64(42), int64(7), "任务一", "HTTP", int64(2), "tenant-a"},
			{int64(43), int64(7), "任务二", "HTTP", int64(2), "tenant-a"},
		}, wantErr: enrich.ErrInvalidDataSourceResponse},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
				if test.queryErr != nil {
					return nil, test.queryErr
				}
				return &readerTestRows{columns: []string{"id", "task_id", "name", "protocol", "bk_biz_id", "bk_tenant_id"}, values: test.values}, nil
			})
			client, err := NewUptimeClient(UptimeClientConfig{DB: db})
			if err != nil {
				t.Fatal(err)
			}
			got, found, err := client.GetUptimeTask(context.Background(), "tenant-a", "7")
			if got != (models.UptimeTask{}) || found || !errors.Is(err, test.wantErr) {
				t.Fatalf("GetUptimeTask()=%#v,%v,%v, want zero,false,%v", got, found, err, test.wantErr)
			}
		})
	}
}

func TestUptimeClientRejectsInvalidRequestsWithoutQuery(t *testing.T) {
	t.Parallel()
	var queries atomic.Int64
	db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		queries.Add(1)
		return nil, errors.New("unexpected query")
	})
	client, err := NewUptimeClient(UptimeClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		ctx    context.Context
		tenant string
		taskID string
	}{
		{name: "nil context", tenant: "tenant-a", taskID: "7"},
		{name: "empty tenant", ctx: context.Background(), taskID: "7"},
		{name: "blank tenant", ctx: context.Background(), tenant: " \t", taskID: "7"},
		{name: "empty task", ctx: context.Background(), tenant: "tenant-a"},
		{name: "blank task", ctx: context.Background(), tenant: "tenant-a", taskID: " \n"},
		{name: "nonnumeric task", ctx: context.Background(), tenant: "tenant-a", taskID: "task-7"},
		{name: "zero task", ctx: context.Background(), tenant: "tenant-a", taskID: "0"},
		{name: "negative task", ctx: context.Background(), tenant: "tenant-a", taskID: "-7"},
		{name: "overflow task", ctx: context.Background(), tenant: "tenant-a", taskID: "9223372036854775808"},
		{name: "fractional task", ctx: context.Background(), tenant: "tenant-a", taskID: "7.1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, found, err := client.GetUptimeTask(test.ctx, test.tenant, test.taskID)
			if got != (models.UptimeTask{}) || found || err == nil {
				t.Fatalf("GetUptimeTask()=%#v,%v,%v, want zero,false,error", got, found, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, found, err := client.GetUptimeTask(ctx, "tenant-a", "7")
	if got != (models.UptimeTask{}) || found || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled GetUptimeTask()=%#v,%v,%v", got, found, err)
	}
	if queries.Load() != 0 {
		t.Fatalf("rejected requests issued %d SQL queries", queries.Load())
	}
}

func TestUptimeClientCancelsInFlightQuery(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	db := openReaderTestDB(t, func(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	client, err := NewUptimeClient(UptimeClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, _, err := client.GetUptimeTask(ctx, "tenant-a", "7")
		result <- err
	}()
	select {
	case <-started:
		cancel()
	case <-ctx.Done():
		t.Fatal("query did not start")
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("GetUptimeTask() error=%v, want context.Canceled", err)
	}
}

func TestUptimeClientGetUptimeNode(t *testing.T) {
	t.Parallel()
	db := openReaderTestDB(t, func(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
		query = strings.Join(strings.Fields(strings.ReplaceAll(query, "`", "")), " ")
		if !strings.HasPrefix(query, "SELECT id,name,plat_id,ip,bk_tenant_id FROM home_application_uptimechecknode ") ||
			!strings.Contains(query, "WHERE bk_tenant_id = ? AND plat_id = ? AND ip = ? AND is_deleted = ?") {
			t.Errorf("unexpected query: %s", query)
		}
		if len(args) < 4 || args[0].Value != "tenant-a" || args[1].Value != int64(0) ||
			args[2].Value != "10.0.0.8" || args[3].Value != false {
			t.Errorf("query args=%#v", args)
		}
		return &readerTestRows{
			columns: []string{"id", "name", "plat_id", "ip", "bk_tenant_id"},
			values:  [][]driver.Value{{int64(8), "北京节点", int64(0), "10.0.0.8", "tenant-a"}},
		}, nil
	})
	client, err := NewUptimeClient(UptimeClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := client.GetUptimeNode(context.Background(), "tenant-a", "0:10.0.0.8")
	want := models.UptimeNode{ID: 8, Name: "北京节点", PlatID: 0, IP: "10.0.0.8", BKTenantID: "tenant-a"}
	if err != nil || !found || got != want {
		t.Fatalf("GetUptimeNode()=%#v,%v,%v, want %#v,true,nil", got, found, err, want)
	}
}

func TestUptimeClientGetUptimeNodeMissingAndInvalidResponse(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		values  [][]driver.Value
		wantErr error
	}{
		{name: "missing"},
		{name: "duplicate", values: [][]driver.Value{
			{int64(8), "北京节点", int64(0), "10.0.0.8", "tenant-a"},
			{int64(9), "北京节点二", int64(0), "10.0.0.8", "tenant-a"},
		}, wantErr: enrich.ErrInvalidDataSourceResponse},
		{name: "tenant mismatch", values: [][]driver.Value{{int64(8), "北京节点", int64(0), "10.0.0.8", "tenant-b"}}, wantErr: enrich.ErrInvalidDataSourceResponse},
		{name: "identity mismatch", values: [][]driver.Value{{int64(8), "北京节点", int64(1), "10.0.0.8", "tenant-a"}}, wantErr: enrich.ErrInvalidDataSourceResponse},
		{name: "empty name", values: [][]driver.Value{{int64(8), "", int64(0), "10.0.0.8", "tenant-a"}}, wantErr: enrich.ErrInvalidDataSourceResponse},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
				return &readerTestRows{
					columns: []string{"id", "name", "plat_id", "ip", "bk_tenant_id"}, values: test.values,
				}, nil
			})
			client, err := NewUptimeClient(UptimeClientConfig{DB: db})
			if err != nil {
				t.Fatal(err)
			}
			got, found, err := client.GetUptimeNode(context.Background(), "tenant-a", "0:10.0.0.8")
			if got != (models.UptimeNode{}) || found || !errors.Is(err, test.wantErr) {
				t.Fatalf("GetUptimeNode()=%#v,%v,%v, want zero,false,%v", got, found, err, test.wantErr)
			}
		})
	}
}

func TestUptimeClientGetUptimeNodeRejectsInvalidIdentity(t *testing.T) {
	t.Parallel()
	var queries atomic.Int64
	db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		queries.Add(1)
		return nil, errors.New("unexpected query")
	})
	client, err := NewUptimeClient(UptimeClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	for _, nodeID := range []string{"", "10.0.0.8", "x:10.0.0.8", "-1:10.0.0.8", "0:"} {
		if _, found, err := client.GetUptimeNode(context.Background(), "tenant-a", nodeID); err == nil || found {
			t.Fatalf("GetUptimeNode(%q) found=%v error=%v", nodeID, found, err)
		}
	}
	if queries.Load() != 0 {
		t.Fatalf("invalid node identities issued %d SQL queries", queries.Load())
	}
}

func TestUptimeClientConcurrentReads(t *testing.T) {
	t.Parallel()
	var queries atomic.Int64
	db := openReaderTestDB(t, func(_ context.Context, _ string, args []driver.NamedValue) (driver.Rows, error) {
		queries.Add(1)
		return &readerTestRows{
			columns: []string{"id", "task_id", "name", "protocol", "bk_biz_id", "bk_tenant_id"},
			values:  [][]driver.Value{{args[1].Value, args[1].Value, "任务", "HTTP", int64(2), args[0].Value}},
		}, nil
	})
	client, err := NewUptimeClient(UptimeClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	const count = 16
	errorsOut := make(chan error, count)
	var group sync.WaitGroup
	for i := range count {
		group.Go(func() {
			id := int64(i + 1)
			got, found, err := client.GetUptimeTask(context.Background(), "tenant-a", fmt.Sprint(id))
			if err != nil || !found || got != (models.UptimeTask{ID: id, TaskID: id, Name: "任务", Protocol: models.UptimeProtocolHTTP, BKBizID: 2, BKTenantID: "tenant-a"}) {
				errorsOut <- fmt.Errorf("task %d: result=%#v, found=%v: %w", id, got, found, err)
			}
		})
	}
	group.Wait()
	close(errorsOut)
	for err := range errorsOut {
		t.Error(err)
	}
	if queries.Load() != count {
		t.Fatalf("queries=%d, want %d", queries.Load(), count)
	}
}
