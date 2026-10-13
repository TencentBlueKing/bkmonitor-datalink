// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").

package bksql

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func unlimitedBKSQLContext(unlimited bool) context.Context {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "unlimited-bksql"})
	ctx = metadata.WithStrictQueryFailures(ctx)
	if unlimited {
		ctx = promql.WithQueryLimitsDisabled(ctx)
	}
	return ctx
}
func unlimitedBKSQLQuery() *metadata.Query {
	return &metadata.Query{DataSource: "bkdata", TableID: "graph.doris", DB: "graph", Measurement: "doris", Field: "value",
		TimeField:  metadata.TimeField{Name: "dtEventTimeStamp", Type: "long", Unit: "millisecond"},
		Aggregates: metadata.Aggregates{{Name: "count", Window: time.Minute, Dimensions: []string{"src", "dst"}}}}
}
func unlimitedBKSQLConsume(set storage.SeriesSet) (int, error) {
	count := 0
	for set.Next() {
		count++
	}
	return count, set.Err()
}

func TestUnlimitedBKSQLQueryLimitsAndIsolation(t *testing.T) {
	ctx := unlimitedBKSQLContext(true)
	if http.DefaultTransport == httpmock.DefaultTransport {
		httpmock.Deactivate()
		t.Cleanup(httpmock.Activate)
	}
	var mu sync.Mutex
	var statements []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request Params
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		statements = append(statements, request.SQL)
		mu.Unlock()
		if !strings.HasPrefix(request.SQL, "SELECT ") {
			_, _ = fmt.Fprint(w, `{"result":true,"code":"00","data":{"list":[{"Field":"dtEventTimeStamp","Type":"bigint"},{"Field":"src","Type":"varchar"},{"Field":"dst","Type":"varchar"},{"Field":"value","Type":"double"}]}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"result":true,"code":"00","data":{"totalRecords":3,"list":[{"src":"a","dst":"b","_value_":1,"_timestamp_":1700000000000},{"src":"a","dst":"c","_value_":1,"_timestamp_":1700000000000},{"src":"a","dst":"d","_value_":1,"_timestamp_":1700000000000}]}}`)
	}))
	defer server.Close()
	i, err := NewInstance(ctx, &Options{Address: server.URL, Timeout: time.Second, MaxLimit: 1, Tolerance: 1, Curl: &curl.HttpCurl{}})
	require.NoError(t, err)
	type outcome struct {
		unlimited bool
		count     int
		err       error
	}
	ch := make(chan outcome, 2)
	for _, unlimited := range []bool{false, true} {
		go func(unlimited bool) {
			count, err := unlimitedBKSQLConsume(i.QuerySeriesSet(unlimitedBKSQLContext(unlimited), unlimitedBKSQLQuery(), time.Unix(1700000000, 0), time.Unix(1700000060, 0)))
			ch <- outcome{unlimited, count, err}
		}(unlimited)
	}
	for n := 0; n < 2; n++ {
		got := <-ch
		if got.unlimited {
			require.NoError(t, got.err)
			require.Equal(t, 3, got.count)
		} else {
			require.ErrorContains(t, got.err, "超过限制")
		}
	}
	q := unlimitedBKSQLQuery()
	q.Size = 9
	_, err = unlimitedBKSQLConsume(i.QuerySeriesSet(ctx, q, time.Unix(1700000000, 0), time.Unix(1700000060, 0)))
	require.NoError(t, err)
	mu.Lock()
	var dataStatements []string
	for _, sql := range statements {
		if strings.HasPrefix(sql, "SELECT ") {
			dataStatements = append(dataStatements, sql)
		}
	}
	mu.Unlock()
	require.Len(t, dataStatements, 3)
	limited, unlimited, explicit := false, false, false
	for _, sql := range dataStatements {
		limited = limited || strings.Contains(sql, " LIMIT 2")
		unlimited = unlimited || !strings.Contains(sql, " LIMIT ")
		explicit = explicit || strings.Contains(sql, " LIMIT 9")
	}
	require.True(t, limited)
	require.True(t, unlimited)
	require.True(t, explicit)
	require.Equal(t, 1, i.maxLimit)
}

func TestUnlimitedBKSQLDeadlineAndParentCancellation(t *testing.T) {
	ctx := unlimitedBKSQLContext(true)
	if http.DefaultTransport == httpmock.DefaultTransport {
		httpmock.Deactivate()
		t.Cleanup(httpmock.Activate)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(80 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		_, _ = fmt.Fprint(w, `{"result":true,"code":"00","data":{"totalRecords":0,"list":[]}}`)
	}))
	defer server.Close()
	i, err := NewInstance(ctx, &Options{Address: server.URL, Timeout: 20 * time.Millisecond, Curl: &curl.HttpCurl{}})
	require.NoError(t, err)
	for _, unlimited := range []bool{false, true} {
		_, err = i.sqlQuery(unlimitedBKSQLContext(unlimited), QuerySyncRequest{SQL: "SELECT value FROM graph"})
		if unlimited {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, context.DeadlineExceeded)
		}
	}
	parent, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	_, err = i.sqlQuery(parent, QuerySyncRequest{SQL: "SELECT value FROM graph"})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorIs(t, parent.Err(), context.DeadlineExceeded)
	canceled, stop := context.WithCancel(ctx)
	stop()
	_, err = i.sqlQuery(canceled, QuerySyncRequest{SQL: "SELECT value FROM graph"})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 20*time.Millisecond, i.timeout)
}
