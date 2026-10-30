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
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jarcoal/httpmock"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/endpoint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/middleware"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

type exampleHTTPRecord struct {
	Path       string          `json:"path"`
	Space      string          `json:"space"`
	Request    json.RawMessage `json:"request"`
	HTTPStatus int             `json:"http_status"`
	Response   json.RawMessage `json:"response"`
}

type exampleResponseWriter struct {
	gin.ResponseWriter
	body bytes.Buffer
}

func (w *exampleResponseWriter) Write(data []byte) (int, error) {
	w.body.Write(data)
	return w.ResponseWriter.Write(data)
}

func (w *exampleResponseWriter) WriteString(data string) (int, error) {
	w.body.WriteString(data)
	return w.ResponseWriter.WriteString(data)
}

type exampleSeries struct {
	metric     string
	dimensions map[string]string
	frames     []int
}

func exampleRelationSeries() []exampleSeries {
	result := make([]exampleSeries, 0)
	add := func(metric string, dimensions map[string]string, frames ...int) {
		dimensions["bcs_cluster_id"] = "example-cluster"
		if len(frames) == 0 {
			frames = []int{0, 1, 2}
		}
		result = append(result, exampleSeries{metric, dimensions, frames})
	}
	for _, suffix := range []string{"a", "b"} {
		add("node_with_pod_relation", map[string]string{"node": "node-" + suffix, "namespace": "demo", "pod": "pod-" + suffix})
		add("container_with_pod_relation", map[string]string{"namespace": "demo", "pod": "pod-" + suffix, "container": "app-" + suffix})
		add("pod_with_replicaset_relation", map[string]string{"namespace": "demo", "pod": "pod-" + suffix, "replicaset": "web-rs"})
	}
	add("deployment_with_replicaset_relation", map[string]string{"namespace": "demo", "deployment": "web", "replicaset": "web-rs"})
	add("container_info_relation", map[string]string{"namespace": "demo", "pod": "pod-a", "container": "app-a", "version": "v1"}, 0)
	add("container_info_relation", map[string]string{"namespace": "demo", "pod": "pod-a", "container": "app-a", "version": "v2"}, 1, 2)
	add("container_info_relation", map[string]string{"namespace": "demo", "pod": "pod-b", "container": "app-b", "version": "v2"})
	return result
}

// Match the rendered selectors against synthetic already-aggregated samples.
// This exercises production routing/PromQL construction, not a VM execution engine.
func exampleVMResponse(request *http.Request, series []exampleSeries) (*http.Response, error) {
	var body mock.VmRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		return nil, err
	}
	var params mock.VmParams
	if err := json.Unmarshal([]byte(body.Sql), &params); err != nil {
		return nil, err
	}
	expression, _ := params.ApiParams["query"].(string)
	expr, err := parser.ParseExpr(expression)
	if err != nil {
		return nil, err
	}
	var selectors []*parser.VectorSelector
	parser.Inspect(expr, func(node parser.Node, _ []parser.Node) error {
		if selector, ok := node.(*parser.VectorSelector); ok {
			selectors = append(selectors, selector)
		}
		return nil
	})
	for _, selector := range selectors {
		if condition := params.MetricFilterCondition[selector.Name]; condition != "" {
			expanded, parseErr := parser.ParseExpr("{" + condition + "}")
			if parseErr != nil {
				return nil, parseErr
			}
			selector.LabelMatchers = expanded.(*parser.VectorSelector).LabelMatchers
		}
	}
	start, _ := params.ApiParams["start"].(float64)
	end, _ := params.ApiParams["end"].(float64)
	step, _ := params.ApiParams["step"].(float64)
	if params.ApiType == "query" {
		start, _ = params.ApiParams["time"].(float64)
		end = start
		step = 60
	}
	if step <= 0 {
		return nil, fmt.Errorf("invalid fixture step: %s", expression)
	}
	matrix := make([]any, 0)
	space := metadata.GetUser(request.Context()).SpaceUID
	if space == "example-backend-error" {
		return httpmock.NewStringResponse(http.StatusServiceUnavailable, "fixture backend unavailable"), nil
	}
	for _, row := range series {
		if space == "example-empty-info" && row.metric == "container_info_relation" {
			continue
		}
		matched := false
		for _, selector := range selectors {
			current := true
			for _, matcher := range selector.LabelMatchers {
				value := row.dimensions[matcher.Name]
				if matcher.Name == labels.MetricName {
					value = row.metric
					if matcher.Matches(row.metric + "_value") {
						continue
					}
				}
				if matcher.Name == "result_table_id" {
					value = "example_relations"
				}
				if !matcher.Matches(value) {
					current = false
					break
				}
			}
			matched = matched || current
		}
		if !matched {
			continue
		}
		values := make([]any, 0)
		for _, frame := range row.frames {
			ts := float64(1700000000 + frame*60)
			if ts >= start && ts <= end {
				values = append(values, []any{ts, "1"})
			}
		}
		if len(values) > 0 {
			sample := map[string]any{"metric": row.dimensions, "values": values}
			if params.ApiType == "query" {
				delete(sample, "values")
				sample["value"] = values[0]
			}
			matrix = append(matrix, sample)
		}
	}
	resultType := "matrix"
	if params.ApiType == "query" {
		resultType = "vector"
	}
	return httpmock.NewJsonResponse(http.StatusOK, map[string]any{
		"result": true, "code": "00", "message": "OK",
		"data": map[string]any{"list": []any{map[string]any{
			"status": "success", "isPartial": space == "example-partial",
			"data": map[string]any{"resultType": resultType, "result": matrix},
		}}},
	})
}

func TestTopologyConsumerExampleHTTP(t *testing.T) {
	for _, yolo := range []bool{false, true} {
		t.Run(fmt.Sprintf("yolo=%t", yolo), func(t *testing.T) { runTopologyConsumerExampleHTTP(t, yolo) })
	}
}

func TestPathResourcesHTTPQualityIsolation(t *testing.T) {
	for _, yolo := range []bool{false, true} {
		t.Run(fmt.Sprintf("yolo=%t", yolo), func(t *testing.T) {
			mock.Init()
			promql.MockEngine()
			ctx := metadata.InitHashID(context.Background())
			influxdb.MockSpaceRouter(ctx)
			useRelationTestConfig(t)
			viper.Set(v1beta3.YoloModeConfigPath, yolo)
			v1beta3.LoadConfig()
			model, err := v1beta3.GetModel(ctx)
			require.NoError(t, err)
			provider := v1beta3.NewStaticSchemaProvider()
			model.SetSchemaProvider(provider)
			t.Cleanup(func() { model.SetSchemaProvider(v1beta3.GetSchemaProvider()) })
			router, err := influxdb.GetSpaceTsDbRouter()
			require.NoError(t, err)
			const spaceName = "example-path-quality"
			space := ir.Space{influxdb.ResultTableVM: &ir.SpaceResultTable{TableId: influxdb.ResultTableVM}}
			require.NoError(t, router.Add(ctx, ir.SpaceToResultTableKey, spaceName, &space))
			previous := router.GetResultTable(ctx, influxdb.ResultTableVM, false)
			t.Cleanup(func() {
				require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, influxdb.ResultTableVM, previous))
				require.NoError(t, router.Delete(ctx, ir.SpaceToResultTableKey, spaceName))
			})
			var fields []string
			for _, schema := range provider.ListRelationSchemas(spaceName) {
				metricName := schema.MetricName
				if metricName == "" {
					metricName = string(schema.RelationType) + "_relation"
				}
				if metricName != "service_to_service_flow" {
					fields = append(fields, metricName)
				}
			}
			for _, resource := range provider.ListResourceTypes(spaceName) {
				fields = append(fields, string(resource)+"_info_relation")
			}
			require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, influxdb.ResultTableVM, &ir.ResultTableDetail{
				StorageId: 2, TableId: influxdb.ResultTableVM, VmRt: "example_relations", Fields: fields,
				MeasurementType: redis.BkSplitMeasurement, StorageType: metadata.VictoriaMetricsStorageType,
			}))
			matcher := httpmock.NewMatcher("path-quality", func(r *http.Request) bool {
				return metadata.GetUser(r.Context()).SpaceUID == spaceName
			})
			httpmock.RegisterMatcherResponder(http.MethodPost, mock.BkBaseUrl, matcher, func(r *http.Request) (*http.Response, error) {
				return exampleVMResponse(r, exampleRelationSeries())
			})
			t.Cleanup(func() { httpmock.RegisterMatcherResponder(http.MethodPost, mock.BkBaseUrl, matcher, nil) })
			gin.SetMode(gin.ReleaseMode)
			route := gin.New()
			group := route.Group("/")
			group.Use(middleware.MetaData(&middleware.Params{}), middleware.JwtAuthMiddleware(false, "", nil))
			RegisterRelation(endpoint.NewRegisterHandler(ctx, group))
			for _, path := range []string{RelationPathResources, RelationPathResourcesRange} {
				t.Run(path, func(t *testing.T) {
					item := func(source string, targets []string, paths [][]string, matcher map[string]string) map[string]any {
						return map[string]any{
							"source_type": source, "target_types": targets, "path_resources": paths, "matcher": matcher,
							"timestamp": 1700000000, "start_time": 1700000000, "end_time": 1700000120, "step": "1m", "look_back_delta": "5m",
						}
					}
					healthy := item("node", []string{"pod"}, [][]string{{"node", "pod"}}, map[string]string{"node": "node-a"})
					body, err := json.Marshal(map[string]any{"query_list": []any{
						healthy,
						item("service", []string{"service"}, [][]string{{"service", "service"}}, nil),
						item("deployment", []string{"datasource"}, nil, map[string]string{"deployment": "web"}),
						item("deployment", []string{"datasource", "replicaset"}, nil, map[string]string{"deployment": "web"}),
						healthy,
						item("deployment", []string{"replicaset"}, [][]string{{"deployment", "replicaset"}}, map[string]string{"deployment": "absent"}),
					}})
					require.NoError(t, err)
					request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
					request.Header.Set("X-Bk-Scope-Space-Uid", spaceName)
					response := httptest.NewRecorder()
					route.ServeHTTP(response, request)
					require.Equal(t, http.StatusOK, response.Code, response.Body.String())
					var decoded struct {
						Data []struct {
							Code          int               `json:"code"`
							Message       string            `json:"message"`
							Partial       bool              `json:"partial"`
							PartialReason string            `json:"partial_reason"`
							Results       []json.RawMessage `json:"results"`
						} `json:"data"`
					}
					require.NoError(t, json.Unmarshal(response.Body.Bytes(), &decoded))
					require.Len(t, decoded.Data, 6)
					require.Equal(t, http.StatusOK, decoded.Data[0].Code, response.Body.String())
					require.NotEmpty(t, decoded.Data[0].Results)
					require.False(t, decoded.Data[0].Partial)
					require.Equal(t, decoded.Data[0], decoded.Data[4], "failure and partial items must not contaminate later healthy items")
					require.Equal(t, http.StatusBadRequest, decoded.Data[1].Code)
					require.Contains(t, decoded.Data[1].Message, "required relation has no physical query route")
					require.Empty(t, decoded.Data[1].Results)
					require.Equal(t, http.StatusOK, decoded.Data[2].Code, decoded.Data[2].Message)
					require.Empty(t, decoded.Data[2].Results)
					require.Equal(t, !yolo, decoded.Data[2].Partial)
					if !yolo {
						require.Equal(t, "path_search_depth_limit", decoded.Data[2].PartialReason)
					}
					require.Equal(t, http.StatusOK, decoded.Data[3].Code, decoded.Data[3].Message)
					require.NotEmpty(t, decoded.Data[3].Results)
					require.Equal(t, !yolo, decoded.Data[3].Partial)
					if !yolo {
						require.Equal(t, "path_search_depth_limit", decoded.Data[3].PartialReason)
					}
					require.Equal(t, http.StatusOK, decoded.Data[5].Code)
					require.Empty(t, decoded.Data[5].Results)
					require.False(t, decoded.Data[5].Partial)
				})
			}
		})
	}
}

func runTopologyConsumerExampleHTTP(t *testing.T, yolo bool) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js 22+ is required for the documented consumer acceptance command")
	}
	mock.Init()
	promql.MockEngine()
	ctx := metadata.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)
	useRelationTestConfig(t)
	viper.Set(v1beta3.YoloModeConfigPath, yolo)
	v1beta3.LoadConfig()
	model, err := v1beta3.GetModel(ctx)
	require.NoError(t, err)
	model.SetSchemaProvider(v1beta3.NewStaticSchemaProvider())
	t.Cleanup(func() { model.SetSchemaProvider(v1beta3.GetSchemaProvider()) })
	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err)
	space := ir.Space{influxdb.ResultTableVM: &ir.SpaceResultTable{TableId: influxdb.ResultTableVM}}
	require.NoError(t, router.Add(ctx, ir.SpaceToResultTableKey, "example-space", &space))
	previousTable := router.GetResultTable(ctx, influxdb.ResultTableVM, false)
	t.Cleanup(func() {
		require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, influxdb.ResultTableVM, previousTable))
		require.NoError(t, router.Delete(ctx, ir.SpaceToResultTableKey, "example-space"))
	})
	series := exampleRelationSeries()
	fields := []string{"node_info_relation", "pod_info_relation", "replicaset_info_relation", "deployment_info_relation"}
	for _, row := range series {
		fields = append(fields, row.metric)
	}
	require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, influxdb.ResultTableVM, &ir.ResultTableDetail{
		StorageId: 2, TableId: influxdb.ResultTableVM, VmRt: "example_relations", Fields: fields,
		MeasurementType: redis.BkSplitMeasurement, StorageType: metadata.VictoriaMetricsStorageType,
	}))
	for _, name := range []string{"example-partial", "example-empty-info", "example-backend-error"} {
		require.NoError(t, router.Add(ctx, ir.SpaceToResultTableKey, name, &space))
		name := name
		t.Cleanup(func() { require.NoError(t, router.Delete(ctx, ir.SpaceToResultTableKey, name)) })
	}
	emptySpace := ir.Space{}
	require.NoError(t, router.Add(ctx, ir.SpaceToResultTableKey, "example-no-route", &emptySpace))
	t.Cleanup(func() { require.NoError(t, router.Delete(ctx, ir.SpaceToResultTableKey, "example-no-route")) })
	noInfoTable := *router.GetResultTable(ctx, influxdb.ResultTableVM, false)
	noInfoTable.TableId = "result_table.k8s"
	noInfoTable.Fields = []string{"container_with_pod_relation"}
	previousNoInfo := router.GetResultTable(ctx, noInfoTable.TableId, true)
	require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, noInfoTable.TableId, &noInfoTable))
	noInfoSpace := ir.Space{noInfoTable.TableId: &ir.SpaceResultTable{TableId: noInfoTable.TableId}}
	require.NoError(t, router.Add(ctx, ir.SpaceToResultTableKey, "example-no-info", &noInfoSpace))
	t.Cleanup(func() {
		require.NoError(t, router.Delete(ctx, ir.SpaceToResultTableKey, "example-no-info"))
		if previousNoInfo != nil {
			require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, noInfoTable.TableId, previousNoInfo))
		} else {
			require.NoError(t, router.Delete(ctx, ir.ResultTableDetailKey, noInfoTable.TableId))
		}
	})
	var records []exampleHTTPRecord
	var recordsMu sync.Mutex
	var mu sync.Mutex
	calls, attributeCalls := 0, 0
	matcher := httpmock.NewMatcher("topology-consumer-example", func(r *http.Request) bool {
		return strings.HasPrefix(metadata.GetUser(r.Context()).SpaceUID, "example-")
	})
	httpmock.RegisterMatcherResponder(http.MethodPost, mock.BkBaseUrl, matcher, func(r *http.Request) (*http.Response, error) {
		// Capture the real body without changing the production query path.
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			return nil, err
		}
		r.Body = io.NopCloser(strings.NewReader(string(raw)))
		mu.Lock()
		calls++
		if strings.Contains(string(raw), "v2") {
			attributeCalls++
		}
		mu.Unlock()
		return exampleVMResponse(r, series)
	})
	t.Cleanup(func() { httpmock.RegisterMatcherResponder(http.MethodPost, mock.BkBaseUrl, matcher, nil) })
	gin.SetMode(gin.ReleaseMode)
	route := gin.New()
	var cliJWTSeen, cliNoJWTSeen atomic.Bool
	group := route.Group("/")
	group.Use(func(c *gin.Context) {
		switch c.GetHeader("X-Bk-Scope-Space-Uid") {
		case "example-cli-with-jwt":
			cliJWTSeen.Store(c.GetHeader("X-Bkapi-Jwt") == "example-cli-jwt")
		case "example-cli-without-jwt":
			cliNoJWTSeen.Store(len(c.Request.Header.Values("X-Bkapi-Jwt")) == 0)
		}
		raw, readErr := io.ReadAll(c.Request.Body)
		if readErr != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(raw))
		writer := &exampleResponseWriter{ResponseWriter: c.Writer}
		c.Writer = writer
		c.Next()
		// Only valid JSON bodies belong in the reusable request/response fixture.
		if json.Valid(raw) && json.Valid(writer.body.Bytes()) {
			recordsMu.Lock()
			records = append(records, exampleHTTPRecord{c.Request.URL.Path, c.GetHeader("X-Bk-Scope-Space-Uid"), raw, writer.Status(), append(json.RawMessage(nil), writer.body.Bytes()...)})
			recordsMu.Unlock()
		}
	})
	group.Use(middleware.MetaData(&middleware.Params{}), middleware.JwtAuthMiddleware(false, "", nil))
	RegisterRelation(endpoint.NewRegisterHandler(ctx, group))
	server := httptest.NewServer(route)
	defer server.Close()
	cmd := exec.Command(node, "--test", "../../../docs/examples/topology-client.test.mjs", "../../../docs/examples/topology-run.test.mjs")
	cmd.Env = append(os.Environ(), "TG_EXAMPLE_URL="+server.URL)
	output, err := cmd.CombinedOutput()
	t.Log(string(output))
	require.NoError(t, err)
	require.True(t, cliJWTSeen.Load(), "CLI forwards the configured JWT")
	require.True(t, cliNoJWTSeen.Load(), "CLI omits an unconfigured JWT")
	if os.Getenv("UPDATE_TIMEGRAPH_EXAMPLES") == "1" {
		recordsMu.Lock()
		data, marshalErr := json.MarshalIndent(records, "", "  ")
		recordsMu.Unlock()
		require.NoError(t, marshalErr)
		require.NoError(t, os.WriteFile(filepath.Join("..", "..", "..", "docs", "examples", "topology-responses.json"), append(data, '\n'), 0o644))
	}
	mu.Lock()
	defer mu.Unlock()
	require.Positive(t, calls, "must execute the real backend transport")
	require.Positive(t, attributeCalls, "source_expand_info must reach the rendered backend request")
}

// Keep request-level failures distinct from HTTP-200 per-item failures.
func TestTopologyDocumentedFailures(t *testing.T) {
	log.InitTestLogger()
	useRelationTestConfig(t)
	viper.Set(v1beta3.YoloModeConfigPath, false)
	v1beta3.LoadConfig()
	type failureRecord struct {
		Name       string          `json:"name"`
		HTTPStatus int             `json:"http_status"`
		Response   json.RawMessage `json:"response"`
	}
	var records []failureRecord
	check := func(t *testing.T, name, body, message string, ctx context.Context) {
		t.Helper()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, viper.GetString(RelationV1Beta3TopologyRangeConfigPath), strings.NewReader(body)).WithContext(ctx)
		HandlerAPIRelationV1Beta3TopologyRange(c)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), message)
		require.True(t, json.Valid(w.Body.Bytes()))
		records = append(records, failureRecord{name, w.Code, append(json.RawMessage(nil), w.Body.Bytes()...)})
	}
	check(t, "invalid-json", "{", "error", context.Background())
	t.Run("request-bytes", func(t *testing.T) {
		old := v1beta3.MaxSharedTopologyRequestBytes
		v1beta3.MaxSharedTopologyRequestBytes = 8
		defer func() { v1beta3.MaxSharedTopologyRequestBytes = old }()
		check(t, "request-bytes", `{"query_list":[]}`, "max_topology_request_bytes", context.Background())
	})
	t.Run("query-count", func(t *testing.T) {
		old := v1beta3.MaxSharedTopologyQueries
		v1beta3.MaxSharedTopologyQueries = 1
		defer func() { v1beta3.MaxSharedTopologyQueries = old }()
		check(t, "query-count", `{"query_list":[{},{}]}`, "max_topology_queries", context.Background())
	})
	t.Run("memory-reservation", func(t *testing.T) {
		old := v1beta3.MaxSharedTopologyReservedBytes
		v1beta3.MaxSharedTopologyReservedBytes = 1
		defer func() { v1beta3.MaxSharedTopologyReservedBytes = old }()
		check(t, "memory-reservation", `{"query_list":[]}`, "max_topology_reserved_bytes", context.Background())
	})
	t.Run("queue", func(t *testing.T) {
		old := v1beta3.MaxSharedTopologyConcurrent
		v1beta3.MaxSharedTopologyConcurrent = 1
		defer func() { v1beta3.MaxSharedTopologyConcurrent = old }()
		_, release, err := v1beta3.AcquireSharedTopology(context.Background())
		require.NoError(t, err)
		defer release()
		deadline, cancelDeadline := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancelDeadline()
		check(t, "queue-timeout", `{"query_list":[]}`, "deadline exceeded", deadline)
		waitCtx, cancelWait := context.WithCancel(context.Background())
		var waiting sync.WaitGroup
		waiting.Add(1)
		defer func() { cancelWait(); waiting.Wait() }()
		done := make(chan error, 1)
		go func() {
			defer waiting.Done()
			_, free, err := v1beta3.AcquireSharedTopology(waitCtx)
			if free != nil {
				free()
			}
			done <- err
		}()
		require.Eventually(t, func() bool { return topologyTestAdmissionState(t)["waiters"] == 1 }, time.Second, time.Millisecond)
		check(t, "queue-full", `{"query_list":[]}`, "max_topology_admission_waiters", context.Background())
		cancelWait()
		require.ErrorIs(t, <-done, context.Canceled)
	})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	check(t, "canceled-before-query-writable-transport", `{"query_list":[]}`, "context canceled", canceled)
	if os.Getenv("UPDATE_TIMEGRAPH_EXAMPLES") == "1" {
		data, err := json.MarshalIndent(records, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile("../../../docs/examples/topology-errors.json", append(data, '\n'), 0o644))
	}
	require.Equal(t, map[string]int64{"active": 0, "waiters": 0, "reserved": 0, "materialized": 0}, topologyTestAdmissionState(t))
}
