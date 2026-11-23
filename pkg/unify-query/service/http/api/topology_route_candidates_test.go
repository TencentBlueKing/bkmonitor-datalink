// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/endpoint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/middleware"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

// routeReuseFillerTables are tables without any relation metric added to each example space: a whole-space scan
// reads them, no route may come from them.
const routeReuseFillerTables = 300

// TestTopologyRecordedResponsesWithRouteCandidates replays the recorded consumer example (docs/examples/
// topology-responses.json, written by TestTopologyConsumerExampleHTTP through the production route resolution) with
// tables lacking every relation metric added to its spaces. Each response is the recorded one, and each topology
// request resolved a relation metric's tables once: its handler span counts the whole-space scans and the
// resolutions that reused one, and they add up to the routed queries it rendered.
//
// ROUTE_REUSE_BACKEND_OUT=<file> also writes every backend request each replayed request rendered (result table
// lists sorted), to compare two builds' routing on the same input.
func TestTopologyRecordedResponsesWithRouteCandidates(t *testing.T) {
	mock.Init()
	promql.MockEngine()
	ctx := metadata.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)
	useRelationTestConfig(t)
	model, err := v1beta3.GetModel(ctx)
	require.NoError(t, err)
	model.SetSchemaProvider(v1beta3.NewStaticSchemaProvider())
	t.Cleanup(func() { model.SetSchemaProvider(v1beta3.GetSchemaProvider()) })
	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err)

	// The consumer example's fixture (TestTopologyConsumerExampleHTTP), with filler tables in each space.
	series := exampleRelationSeries()
	fields := []string{"node_info_relation", "pod_info_relation", "replicaset_info_relation", "deployment_info_relation"}
	for _, row := range series {
		fields = append(fields, row.metric)
	}
	previousTable := router.GetResultTable(ctx, influxdb.ResultTableVM, false)
	require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, influxdb.ResultTableVM, &ir.ResultTableDetail{
		StorageId: 2, TableId: influxdb.ResultTableVM, VmRt: "example_relations", Fields: fields,
		MeasurementType: redis.BkSplitMeasurement, StorageType: metadata.VictoriaMetricsStorageType,
	}))
	t.Cleanup(func() {
		require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, influxdb.ResultTableVM, previousTable))
	})
	withFillers := func(space ir.Space) *ir.Space {
		for i := 0; i < routeReuseFillerTables; i++ {
			id := fmt.Sprintf("route_reuse_filler_%03d.__default__", i)
			space[id] = &ir.SpaceResultTable{TableId: id}
		}
		return &space
	}
	for i := 0; i < routeReuseFillerTables; i++ {
		id := fmt.Sprintf("route_reuse_filler_%03d.__default__", i)
		require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, id, &ir.ResultTableDetail{
			StorageId: 2, TableId: id, VmRt: fmt.Sprintf("route_reuse_filler_%03d", i),
			Fields:          []string{fmt.Sprintf("filler_metric_%03d", i), "container_cpu_usage_seconds_total"},
			MeasurementType: redis.BkSplitMeasurement, StorageType: metadata.VictoriaMetricsStorageType,
		}))
		t.Cleanup(func() { require.NoError(t, router.Delete(ctx, ir.ResultTableDetailKey, id)) })
	}
	addSpace := func(name string, space *ir.Space) {
		require.NoError(t, router.Add(ctx, ir.SpaceToResultTableKey, name, space))
		t.Cleanup(func() { require.NoError(t, router.Delete(ctx, ir.SpaceToResultTableKey, name)) })
	}
	for _, name := range []string{"example-space", "example-partial", "example-empty-info", "example-backend-error"} {
		addSpace(name, withFillers(ir.Space{influxdb.ResultTableVM: &ir.SpaceResultTable{TableId: influxdb.ResultTableVM}}))
	}
	addSpace("example-no-route", withFillers(ir.Space{}))
	noInfoTable := *router.GetResultTable(ctx, influxdb.ResultTableVM, false)
	noInfoTable.TableId = "result_table.k8s"
	noInfoTable.Fields = []string{"container_with_pod_relation"}
	previousNoInfo := router.GetResultTable(ctx, noInfoTable.TableId, true)
	require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, noInfoTable.TableId, &noInfoTable))
	t.Cleanup(func() {
		if previousNoInfo != nil {
			require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, noInfoTable.TableId, previousNoInfo))
		} else {
			require.NoError(t, router.Delete(ctx, ir.ResultTableDetailKey, noInfoTable.TableId))
		}
	})
	addSpace("example-no-info", withFillers(ir.Space{noInfoTable.TableId: &ir.SpaceResultTable{TableId: noInfoTable.TableId}}))

	var backendMu sync.Mutex
	var backend []string
	matcher := httpmock.NewMatcher("topology-route-candidates", func(r *http.Request) bool {
		return strings.HasPrefix(metadata.GetUser(r.Context()).SpaceUID, "example-")
	})
	httpmock.RegisterMatcherResponder(http.MethodPost, mock.BkBaseUrl, matcher, func(r *http.Request) (*http.Response, error) {
		raw, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			return nil, readErr
		}
		var body mock.VmRequest
		var params mock.VmParams
		if err := json.Unmarshal(raw, &body); err == nil && json.Unmarshal([]byte(body.Sql), &params) == nil {
			sort.Strings(params.ResultTableList)
			normalized, _ := json.Marshal(params)
			backendMu.Lock()
			backend = append(backend, string(normalized))
			backendMu.Unlock()
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return exampleVMResponse(r, series)
	})
	t.Cleanup(func() { httpmock.RegisterMatcherResponder(http.MethodPost, mock.BkBaseUrl, matcher, nil) })

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		require.NoError(t, provider.Shutdown(context.Background()))
	})

	gin.SetMode(gin.ReleaseMode)
	route := gin.New()
	group := route.Group("/")
	group.Use(middleware.MetaData(&middleware.Params{}), middleware.JwtAuthMiddleware(false, "", nil))
	RegisterRelation(endpoint.NewRegisterHandler(ctx, group))
	server := httptest.NewServer(route)
	defer server.Close()
	// Its own transport: mock.Init routes the default one to the backend fixtures.
	client := &http.Client{Transport: &http.Transport{}}

	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "examples", "topology-responses.json"))
	require.NoError(t, err)
	var records []exampleHTTPRecord
	require.NoError(t, json.Unmarshal(raw, &records))
	require.NotEmpty(t, records)
	type rendered struct {
		Path    string   `json:"path"`
		Space   string   `json:"space"`
		Backend []string `json:"backend"`
	}
	var renders []rendered
	topologyRequests, reused, scanned := 0, 0, 0
	for index, record := range records {
		backendMu.Lock()
		backend = nil
		backendMu.Unlock()
		before := len(recorder.Ended())
		request, err := http.NewRequest(http.MethodPost, server.URL+record.Path, bytes.NewReader(record.Request))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Bk-Scope-Space-Uid", record.Space)
		response, err := client.Do(request)
		require.NoError(t, err)
		body, err := io.ReadAll(response.Body)
		require.NoError(t, response.Body.Close())
		require.NoError(t, err)
		require.Equal(t, record.HTTPStatus, response.StatusCode, "record %d", index)
		expected, actual := responseForComparison(t, record.Response, record.HTTPStatus), responseForComparison(t, body, response.StatusCode)
		require.JSONEq(t, string(expected), string(actual), "record %d %s %s", index, record.Path, record.Space)
		backendMu.Lock()
		sort.Strings(backend)
		renders = append(renders, rendered{Path: record.Path, Space: record.Space, Backend: append([]string(nil), backend...)})
		backendMu.Unlock()

		if !strings.HasSuffix(record.Path, "/topology_range") && !strings.HasSuffix(record.Path, "/topology") {
			continue
		}
		// This request's spans: its routed queries and its handler's reuse counts.
		resolutions, scans, hits := 0, -1, -1
		for _, span := range recorder.Ended()[before:] {
			switch span.Name() {
			case "query-ts-to-query-metric":
				resolutions++
			case "handler-api-relation-v1beta3-topology-range", "handler-api-relation-v1beta3-topology":
				for _, attribute := range span.Attributes() {
					switch string(attribute.Key) {
					case "route-candidate-scans":
						scans = int(attribute.Value.AsInt64())
					case "route-candidate-hits":
						hits = int(attribute.Value.AsInt64())
					}
				}
			}
		}
		require.GreaterOrEqual(t, scans, 0, "record %d: handler span counts its scans", index)
		require.GreaterOrEqual(t, hits, 0, "record %d: handler span counts its reuses", index)
		require.Equal(t, resolutions, scans+hits, "record %d: every routed query scanned or reused", index)
		topologyRequests++
		reused += hits
		scanned += scans
	}
	t.Logf("topology requests %d, whole-space scans %d, reused %d", topologyRequests, scanned, reused)
	require.Positive(t, topologyRequests)
	require.Positive(t, reused, "a request resolving a relation metric twice reuses its tables")
	if out := os.Getenv("ROUTE_REUSE_BACKEND_OUT"); out != "" {
		data, err := json.MarshalIndent(renders, "", " ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(out, append(data, '\n'), 0o644))
	}
}

// responseForComparison drops the response's trace id (the recording ran without a tracer, this replay records
// spans) and, for a rejected request, the decoder's own wording: its text depends on the JSON library built in.
func responseForComparison(t *testing.T, body []byte, status int) []byte {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		return body
	}
	delete(value, "trace_id")
	if status != http.StatusOK {
		message, _ := value["error"].(string)
		require.NotEmpty(t, message, "a rejected request says why")
		value["error"] = "<decoder message>"
	}
	normalized, err := json.Marshal(value)
	require.NoError(t, err)
	return normalized
}
