// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

const (
	onlineQueryGoldenCasesRoot  = "../../internal/online_cases/query_golden_cases/testdata/cases"
	onlineQueryGoldenESURL      = "http://127.0.0.1:93012"
	onlineQueryGoldenInfluxHost = "127.0.0.1"
	onlineQueryGoldenInfluxPort = 12312
)

type onlineQueryGoldenCase struct {
	ID      string `yaml:"id"`
	Storage string `yaml:"storage"`
	Enabled *bool  `yaml:"enabled"`
	Files   struct {
		Request      string `yaml:"request"`
		Route        string `yaml:"route"`
		Dependencies string `yaml:"dependencies"`
		ExpectOutput string `yaml:"expect_outputs"`
	} `yaml:"files"`
	caseDir string
}

type onlineQueryGoldenHTTPRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
}

type onlineQueryGoldenRoute struct {
	SpaceUID           string                           `json:"space_uid"`
	ResultTables       []*ir.SpaceResultTable           `json:"result_tables"`
	ResultTableDetails map[string]*ir.ResultTableDetail `json:"result_table_details"`
	DataLabels         map[string]ir.ResultTableList    `json:"data_labels"`
	Storages           map[string]struct {
		Type string `json:"type"`
	} `json:"storages"`
	MustVMTableIDs []string `json:"must_vm_table_ids"`
}

type onlineQueryGoldenDependencies struct {
	VM struct {
		ResultType string `json:"result_type"`
		Result     any    `json:"result"`
	} `json:"vm"`
	BKSQL struct {
		Schema      []map[string]any            `json:"schema"`
		SchemaBySQL map[string][]map[string]any `json:"schema_by_sql"`
		Result      []map[string]any            `json:"result"`
	} `json:"bksql"`
	Elasticsearch struct {
		Mapping json.RawMessage `json:"mapping"`
		Search  json.RawMessage `json:"search"`
	} `json:"elasticsearch"`
	InfluxDB json.RawMessage `json:"influxdb"`
}

type onlineQueryGoldenOutput struct {
	Backend string              `json:"backend"`
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Query   map[string][]string `json:"query,omitempty"`
	Body    any                 `json:"body,omitempty"`
}

type onlineQueryGoldenRecorder struct {
	mu      sync.Mutex
	outputs []onlineQueryGoldenOutput
}

func (r *onlineQueryGoldenRecorder) add(output onlineQueryGoldenOutput) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outputs = append(r.outputs, output)
}

func (r *onlineQueryGoldenRecorder) snapshot() []onlineQueryGoldenOutput {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]onlineQueryGoldenOutput(nil), r.outputs...)
}

func TestOnlineQueryGoldenCases(t *testing.T) {
	cases := loadOnlineQueryGoldenCases(t)
	require.NotEmpty(t, cases)
	setupOnlineQueryGoldenEnvironment(t, cases)

	for _, tc := range cases {
		t.Run(tc.ID, func(t *testing.T) {
			runOnlineQueryGoldenCase(t, tc)
		})
	}
}

func setupOnlineQueryGoldenEnvironment(t *testing.T, cases []onlineQueryGoldenCase) {
	t.Helper()
	ctx := metadata.InitHashID(context.Background())
	mock.Init()
	promql.MockEngine()
	influxdb.MockSpaceRouter(ctx)
	mockOnlineQueryGoldenFeatureFlag(t, ctx, collectOnlineQueryGoldenMustVMTableIDs(t, cases))
	t.Cleanup(func() {
		mockOnlineQueryGoldenFeatureFlag(t, ctx, nil)
	})
}

func runOnlineQueryGoldenCase(t *testing.T, tc onlineQueryGoldenCase) {
	t.Helper()

	expected := readOnlineQueryGoldenJSON[[]onlineQueryGoldenOutput](t, tc.caseDir, tc.Files.ExpectOutput)
	actual := captureOnlineQueryGoldenOutputs(t, tc, nil)
	require.Equal(t, canonicalOnlineQueryGoldenOutputs(t, expected), canonicalOnlineQueryGoldenOutputs(t, actual))
}

func captureOnlineQueryGoldenOutputs(
	t *testing.T, tc onlineQueryGoldenCase, routeOverride *onlineQueryGoldenRoute,
) []onlineQueryGoldenOutput {
	t.Helper()

	request := readOnlineQueryGoldenJSON[onlineQueryGoldenHTTPRequest](t, tc.caseDir, tc.Files.Request)
	route := readOnlineQueryGoldenJSON[onlineQueryGoldenRoute](t, tc.caseDir, tc.Files.Route)
	if routeOverride != nil {
		route = *routeOverride
	}
	dependencies := readOnlineQueryGoldenJSON[onlineQueryGoldenDependencies](t, tc.caseDir, tc.Files.Dependencies)
	if tc.Storage == metadata.InfluxDBStorageType {
		require.NotEmpty(t, dependencies.InfluxDB)
		require.True(t, json.Valid(dependencies.InfluxDB))
	}

	ctx := metadata.InitHashID(context.Background())
	restoreInfluxRouter := useOnlineQueryGoldenInfluxRouter(tc.Storage)
	defer restoreInfluxRouter()
	applyOnlineQueryGoldenRoute(t, ctx, route)
	metadata.SetUser(ctx, &metadata.User{
		Key:      request.Headers["Bk-Query-Source"],
		SpaceUID: request.Headers["X-Bk-Scope-Space-Uid"],
		TenantID: request.Headers["X-Bk-Tenant-Id"],
	})

	recorder := &onlineQueryGoldenRecorder{}
	registerOnlineQueryGoldenResponders(t, dependencies, recorder)
	defer unregisterOnlineQueryGoldenResponders()

	req, err := http.NewRequestWithContext(ctx, request.Method, request.Path, bytes.NewReader(request.Body))
	require.NoError(t, err)
	for key, value := range request.Headers {
		req.Header.Set(key, value)
	}

	gin.SetMode(gin.TestMode)
	w := &Writer{}
	c := &gin.Context{Request: req, Writer: w}
	switch request.Path {
	case "/query/ts/promql":
		HandlerQueryPromQL(c)
	case "/query/ts":
		HandlerQueryTs(c)
	case "/query/ts/raw":
		HandlerQueryRaw(c)
	case "/query/ts/info/field_map":
		HandlerFieldMap(c)
	default:
		t.Fatalf("unsupported golden request path %q", request.Path)
	}

	assertOnlineQueryGoldenResponseSucceeded(t, w.body())
	return recorder.snapshot()
}

func TestOnlineQueryGoldenSegmentedRouteControlsFanOut(t *testing.T) {
	cases := loadOnlineQueryGoldenCases(t)
	setupOnlineQueryGoldenEnvironment(t, cases)

	var tc onlineQueryGoldenCase
	for _, candidate := range cases {
		if candidate.ID == "doris_es_segmented_multi_output_001" {
			tc = candidate
			break
		}
	}
	require.NotEmpty(t, tc.ID)

	route := readOnlineQueryGoldenJSON[onlineQueryGoldenRoute](t, tc.caseDir, tc.Files.Route)
	removedESSegment := false
	for _, detail := range route.ResultTableDetails {
		records := detail.StorageClusterRecords[:0]
		for _, record := range detail.StorageClusterRecords {
			if record.StorageType == metadata.ElasticsearchStorageType {
				removedESSegment = true
				continue
			}
			records = append(records, record)
		}
		detail.StorageClusterRecords = records
	}
	require.True(t, removedESSegment)

	outputs := captureOnlineQueryGoldenOutputs(t, tc, &route)
	require.Len(t, outputs, 2)
	for _, output := range outputs {
		require.Equal(t, "bkbase", output.Backend)
	}
}

func loadOnlineQueryGoldenCases(t *testing.T) []onlineQueryGoldenCase {
	t.Helper()

	var cases []onlineQueryGoldenCase
	err := filepath.WalkDir(onlineQueryGoldenCasesRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() != "case.yaml" {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var tc onlineQueryGoldenCase
		if err := yaml.Unmarshal(content, &tc); err != nil {
			return err
		}
		if tc.Enabled != nil && !*tc.Enabled {
			return nil
		}
		tc.caseDir = filepath.Dir(path)
		cases = append(cases, tc)
		return nil
	})
	require.NoError(t, err)
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases
}

func readOnlineQueryGoldenJSON[T any](t *testing.T, caseDir, name string) T {
	t.Helper()

	var value T
	content, err := os.ReadFile(filepath.Join(caseDir, name))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(content, &value))
	return value
}

func collectOnlineQueryGoldenMustVMTableIDs(t *testing.T, cases []onlineQueryGoldenCase) []string {
	t.Helper()

	var tableIDs []string
	for _, tc := range cases {
		route := readOnlineQueryGoldenJSON[onlineQueryGoldenRoute](t, tc.caseDir, tc.Files.Route)
		tableIDs = append(tableIDs, route.MustVMTableIDs...)
	}
	sort.Strings(tableIDs)
	return tableIDs
}

func applyOnlineQueryGoldenRoute(t *testing.T, ctx context.Context, fixture onlineQueryGoldenRoute) {
	t.Helper()

	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err)
	space := make(ir.Space, len(fixture.ResultTables))
	for _, resultTable := range fixture.ResultTables {
		space[resultTable.TableId] = resultTable
	}
	require.NoError(t, router.Add(ctx, ir.SpaceToResultTableKey, fixture.SpaceUID, &space))
	for tableID, detail := range fixture.ResultTableDetails {
		require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, tableID, detail))
	}
	for dataLabel, resultTables := range fixture.DataLabels {
		resultTables := resultTables
		require.NoError(t, router.Add(ctx, ir.DataLabelToResultTableKey, dataLabel, &resultTables))
	}

	for storageID, fixtureStorage := range fixture.Storages {
		storage := &tsdb.Storage{Type: fixtureStorage.Type}
		switch fixtureStorage.Type {
		case metadata.ElasticsearchStorageType:
			storage.Address = onlineQueryGoldenESURL
		case metadata.BkSqlStorageType, metadata.VictoriaMetricsStorageType:
			storage.Address = mock.BkBaseUrl
		}
		tsdb.SetStorage(storageID, storage)
	}
}

func useOnlineQueryGoldenInfluxRouter(storage string) func() {
	if storage != metadata.InfluxDBStorageType {
		return func() {}
	}
	influxdb.MockRouterWithHostInfo(ir.HostInfo{
		"default": &ir.Host{
			DomainName: onlineQueryGoldenInfluxHost,
			Port:       onlineQueryGoldenInfluxPort,
			Protocol:   "http",
		},
	})
	return func() {
		influxdb.MockRouterWithHostInfo(ir.HostInfo{
			"default": &ir.Host{DomainName: "127.0.0.1", Port: 12302, Protocol: "http"},
		})
	}
}

func registerOnlineQueryGoldenResponders(
	t *testing.T, dependencies onlineQueryGoldenDependencies, recorder *onlineQueryGoldenRecorder,
) {
	t.Helper()

	httpmock.RegisterMatcherResponder(
		http.MethodPost,
		mock.BkBaseUrl,
		onlineQueryGoldenRequestMatcher(),
		func(request *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			normalized, err := normalizeOnlineQueryGoldenBKBaseBody(body)
			if err != nil {
				return nil, err
			}
			recorder.add(onlineQueryGoldenOutput{
				Backend: "bkbase",
				Method:  request.Method,
				Path:    "/query_sync/",
				Body:    normalized,
			})

			sql, _ := normalized["sql"].(string)
			if _, ok := normalized["sql"].(map[string]any); ok {
				return onlineQueryGoldenVMResponse(dependencies)
			}
			if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(sql)), "SHOW ") ||
				strings.HasPrefix(strings.ToUpper(strings.TrimSpace(sql)), "DESC ") {
				schema := dependencies.BKSQL.Schema
				if matched, ok := dependencies.BKSQL.SchemaBySQL[sql]; ok {
					schema = matched
				}
				return onlineQueryGoldenBKSQLResponse(schema)
			}
			return onlineQueryGoldenBKSQLResponse(dependencies.BKSQL.Result)
		},
	)

	httpmock.RegisterResponder(http.MethodHead, onlineQueryGoldenESURL, httpmock.NewStringResponder(http.StatusOK, ""))
	esURLPattern := "=~^" + regexp.QuoteMeta(onlineQueryGoldenESURL) + "/"
	httpmock.RegisterResponder(http.MethodGet, esURLPattern, func(request *http.Request) (*http.Response, error) {
		return onlineQueryGoldenESResponse(request, dependencies, recorder)
	})
	httpmock.RegisterResponder(http.MethodPost, esURLPattern, func(request *http.Request) (*http.Response, error) {
		return onlineQueryGoldenESResponse(request, dependencies, recorder)
	})
	httpmock.RegisterResponder(
		http.MethodGet,
		fmt.Sprintf("http://%s:%d/query", onlineQueryGoldenInfluxHost, onlineQueryGoldenInfluxPort),
		func(request *http.Request) (*http.Response, error) {
			return onlineQueryGoldenInfluxDBResponse(request, dependencies, recorder)
		},
	)
}

func unregisterOnlineQueryGoldenResponders() {
	httpmock.RegisterMatcherResponder(
		http.MethodPost, mock.BkBaseUrl, onlineQueryGoldenRequestMatcher(), nil,
	)
	httpmock.RegisterResponder(http.MethodHead, onlineQueryGoldenESURL, nil)
	esURLPattern := "=~^" + regexp.QuoteMeta(onlineQueryGoldenESURL) + "/"
	httpmock.RegisterResponder(http.MethodGet, esURLPattern, nil)
	httpmock.RegisterResponder(http.MethodPost, esURLPattern, nil)
	httpmock.RegisterResponder(
		http.MethodGet,
		fmt.Sprintf("http://%s:%d/query", onlineQueryGoldenInfluxHost, onlineQueryGoldenInfluxPort),
		nil,
	)
}

func onlineQueryGoldenRequestMatcher() httpmock.Matcher {
	return httpmock.NewMatcher("uq-golden-request", func(request *http.Request) bool {
		return metadata.GetUser(request.Context()).Source == "golden"
	})
}

func normalizeOnlineQueryGoldenBKBaseBody(content []byte) (map[string]any, error) {
	var body map[string]any
	if err := json.Unmarshal(content, &body); err != nil {
		return nil, err
	}
	for _, key := range []string{
		"bk_app_code", "bk_username", "bkdata_authentication_method", "bkdata_data_token", "access_token",
	} {
		delete(body, key)
	}
	if sql, ok := body["sql"].(string); ok {
		var embedded map[string]any
		if json.Unmarshal([]byte(sql), &embedded) == nil {
			body["sql"] = embedded
		}
	}
	return body, nil
}

func onlineQueryGoldenVMResponse(dependencies onlineQueryGoldenDependencies) (*http.Response, error) {
	return httpmock.NewJsonResponse(http.StatusOK, map[string]any{
		"result":  true,
		"message": "OK",
		"code":    "00",
		"data": map[string]any{"list": []any{map[string]any{
			"status": "success", "isPartial": false,
			"data": map[string]any{"resultType": dependencies.VM.ResultType, "result": dependencies.VM.Result},
		}}},
	})
}

func onlineQueryGoldenBKSQLResponse(list []map[string]any) (*http.Response, error) {
	return httpmock.NewJsonResponse(http.StatusOK, map[string]any{
		"result":  true,
		"message": "OK",
		"code":    "00",
		"data":    map[string]any{"list": list, "totalRecords": len(list)},
		"errors":  nil,
	})
}

func onlineQueryGoldenESResponse(
	request *http.Request, dependencies onlineQueryGoldenDependencies, recorder *onlineQueryGoldenRecorder,
) (*http.Response, error) {
	var body any
	if request.Body != nil {
		content, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(content)) > 0 {
			if err := json.Unmarshal(content, &body); err != nil {
				return nil, err
			}
		}
	}
	recorder.add(onlineQueryGoldenOutput{
		Backend: "elasticsearch",
		Method:  request.Method,
		Path:    request.URL.Path,
		Query:   cloneOnlineQueryGoldenQuery(request.URL.Query()),
		Body:    body,
	})

	response := dependencies.Elasticsearch.Search
	if request.Method == http.MethodGet {
		response = dependencies.Elasticsearch.Mapping
	}
	if len(response) == 0 {
		response = json.RawMessage(`{}`)
	}
	return httpmock.NewBytesResponse(http.StatusOK, response), nil
}

func onlineQueryGoldenInfluxDBResponse(
	request *http.Request, dependencies onlineQueryGoldenDependencies, recorder *onlineQueryGoldenRecorder,
) (*http.Response, error) {
	recorder.add(onlineQueryGoldenOutput{
		Backend: "influxdb",
		Method:  request.Method,
		Path:    request.URL.Path,
		Query:   cloneOnlineQueryGoldenQuery(request.URL.Query()),
	})
	response := dependencies.InfluxDB
	if len(response) == 0 {
		response = json.RawMessage(`{"results":[{}]}`)
	}
	var payload any
	if err := json.Unmarshal(response, &payload); err != nil {
		return nil, err
	}
	return httpmock.NewJsonResponse(http.StatusOK, payload)
}

func cloneOnlineQueryGoldenQuery(values url.Values) map[string][]string {
	query := make(map[string][]string, len(values))
	for key, value := range values {
		switch key {
		case "u", "p", "token":
			continue
		}
		query[key] = append([]string(nil), value...)
		sort.Strings(query[key])
	}
	if len(query) == 0 {
		return nil
	}
	return query
}

func assertOnlineQueryGoldenResponseSucceeded(t *testing.T, content string) {
	t.Helper()

	var response map[string]any
	require.NoError(t, json.Unmarshal([]byte(content), &response), content)
	status, ok := response["status"].(map[string]any)
	if !ok {
		return
	}
	if code, _ := status["code"].(string); code != "" {
		t.Fatalf("UQ handler failed: %s", content)
	}
}

func canonicalOnlineQueryGoldenOutputs(t *testing.T, outputs []onlineQueryGoldenOutput) []string {
	t.Helper()

	canonical := make([]string, 0, len(outputs))
	for _, output := range outputs {
		normalizeOnlineQueryGoldenOutput(&output)
		content, err := json.Marshal(output)
		require.NoError(t, err)
		canonical = append(canonical, string(content))
	}
	sort.Strings(canonical)
	return canonical
}

func normalizeOnlineQueryGoldenOutput(output *onlineQueryGoldenOutput) {
	if output.Backend != "bkbase" {
		return
	}
	body, ok := output.Body.(map[string]any)
	if !ok {
		return
	}
	sql, ok := body["sql"].(map[string]any)
	if !ok {
		return
	}
	conditions, ok := sql["metric_filter_condition"].(map[string]any)
	if !ok {
		return
	}
	for reference, value := range conditions {
		expression, ok := value.(string)
		if !ok {
			continue
		}
		clauses := strings.Split(expression, " or ")
		sort.Strings(clauses)
		conditions[reference] = strings.Join(clauses, " or ")
	}
}

func TestCanonicalOnlineQueryGoldenOutputsPreservesMultiplicity(t *testing.T) {
	output := onlineQueryGoldenOutput{Backend: "bkbase", Method: http.MethodPost, Path: "/query_sync/"}
	single := canonicalOnlineQueryGoldenOutputs(t, []onlineQueryGoldenOutput{output})
	duplicates := canonicalOnlineQueryGoldenOutputs(t, []onlineQueryGoldenOutput{output, output})

	require.Equal(t, []string{single[0], single[0]}, duplicates)
}

func TestCanonicalOnlineQueryGoldenOutputsNormalizesVMRouteConditionOrder(t *testing.T) {
	output := func(condition string) onlineQueryGoldenOutput {
		return onlineQueryGoldenOutput{
			Backend: "bkbase",
			Method:  http.MethodPost,
			Path:    "/query_sync/",
			Body: map[string]any{"sql": map[string]any{
				"metric_filter_condition": map[string]any{"a": condition},
			}},
		}
	}

	first := canonicalOnlineQueryGoldenOutputs(t, []onlineQueryGoldenOutput{output(`table="a" or table="b"`)})
	second := canonicalOnlineQueryGoldenOutputs(t, []onlineQueryGoldenOutput{output(`table="b" or table="a"`)})
	require.Equal(t, first, second)
}

func mockOnlineQueryGoldenFeatureFlag(t *testing.T, ctx context.Context, tableIDs []string) {
	t.Helper()

	mustVMTargets := []any{
		map[string]any{
			"query":      `tableID in ["result_table.vm", "result_table.k8s"]`,
			"percentage": map[string]int{"true": 100, "false": 0},
		},
		map[string]any{
			"query":      `tableID in ["system.cpu_detail", "system.disk"]`,
			"percentage": map[string]int{"true": 100, "false": 0},
		},
	}
	if len(tableIDs) > 0 {
		mustVMTableIDs, err := json.Marshal(tableIDs)
		require.NoError(t, err)
		mustVMTargets = append(mustVMTargets, map[string]any{
			"query":      fmt.Sprintf("tableID in %s", string(mustVMTableIDs)),
			"percentage": map[string]int{"true": 100, "false": 0},
		})
	}

	flags := map[string]any{
		"bk-data-table-id-auth": map[string]any{
			"variations": map[string]bool{"true": true, "false": false},
			"targeting": []any{map[string]any{
				"query":      `spaceUID in ["bkdata"]`,
				"percentage": map[string]int{"false": 100},
			}},
			"defaultRule": map[string]string{"variation": "true"},
		},
		"jwt-auth": map[string]any{
			"variations":  map[string]bool{"true": true, "false": false},
			"targeting":   []any{},
			"defaultRule": map[string]string{"variation": "true"},
		},
		"must-vm-query": map[string]any{
			"variations":  map[string]bool{"true": true, "false": false},
			"targeting":   mustVMTargets,
			"defaultRule": map[string]string{"variation": "false"},
		},
	}
	content, err := json.Marshal(flags)
	require.NoError(t, err)
	require.NoError(t, featureFlag.MockFeatureFlag(ctx, string(content)))
}
