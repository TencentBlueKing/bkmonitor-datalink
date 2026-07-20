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
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

const (
	// onlineVMGoldenCasesRoot 保存正式 VM golden case。
	// 每个子目录对应一条线上采样 case，目录内包含 request.json 和 expect.downstream.json。
	onlineVMGoldenCasesRoot = "../../internal/online_cases/query_golden_cases/testdata/cases/vm"
)

// onlineVMGoldenCase 描述一条 VM golden case 在本地测试里需要补齐的路由信息。
// request.json 只保存用户入口请求；线上 router/metadata 不会在本地自动存在，
// 所以这里用最小 router fixture 复原“这条线上查询当时应该路由到哪个 VM RT/集群/字段”。
type onlineVMGoldenCase struct {
	Name              string
	SpaceUID          string
	QuerySource       string
	TenantID          string
	TableID           string
	ResultTableID     string
	StorageID         int64
	StorageCluster    string
	Measurement       string
	Fields            []string
	MeasurementType   string
	DataLabel         string
	WantResultTableID string
}

type onlineVMGoldenHTTPRequest struct {
	Method  string                 `json:"method"`
	Path    string                 `json:"path"`
	Headers map[string]string      `json:"headers"`
	Body    structured.QueryPromQL `json:"body"`
}

type onlineVMGoldenExpect struct {
	Method  string `json:"method"`
	URLPath string `json:"url_path"`
	Body    struct {
		PreferStorage string                 `json:"prefer_storage"`
		SQL           onlineVMGoldenQuerySQL `json:"sql"`
	} `json:"body"`
}

type onlineVMGoldenBKBaseRequest struct {
	PreferStorage string `json:"prefer_storage"`
	SQL           string `json:"sql"`
}

type onlineVMGoldenQuerySQL struct {
	APIType               string            `json:"api_type"`
	ClusterName           string            `json:"cluster_name"`
	APIParams             onlineVMGoldenAPI `json:"api_params"`
	ResultTableList       []string          `json:"result_table_list,omitempty"`
	MetricFilterCondition map[string]string `json:"metric_filter_condition,omitempty"`
}

type onlineVMGoldenAPI struct {
	Query   string `json:"query"`
	Start   int64  `json:"start"`
	End     int64  `json:"end"`
	Step    int64  `json:"step"`
	NoCache int    `json:"nocache"`
}

func TestOnlineVMQueryGoldenCaseRealPath(t *testing.T) {
	// 新增 VM case 时，优先新增 testdata/cases/vm/<case_name>/ 下的三个文件：
	// case.yaml、request.json、expect.downstream.json。
	// 然后在这个 cases 列表里补一项对应的本地 router fixture 信息即可，不需要复制测试函数。
	cases := []onlineVMGoldenCase{
		{
			Name:              "vm_query_builder_real_001",
			SpaceUID:          "bksaas__ai-todq-report",
			QuerySource:       "strategy",
			TenantID:          "system",
			TableID:           "pushgateway_rabbitmq_cluster.group3",
			ResultTableID:     "100147_vm_hgateway_rabbitmq_cluster_group3",
			StorageID:         2,
			StorageCluster:    "monitor-1",
			Measurement:       "group3",
			Fields:            []string{"rabbitmq_instance_queue_messages"},
			MeasurementType:   "bk_split_measurement",
			DataLabel:         "pushgateway_rabbitmq_cluster",
			WantResultTableID: "pushgateway_rabbitmq_cluster.group3",
		},
	}

	ctx := metadata.InitHashID(context.Background())
	mock.Init()
	promql.MockEngine()
	influxdb.MockSpaceRouter(ctx)
	mockOnlineVMGoldenMustVMFeatureFlag(t, ctx, cases)

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			runOnlineVMGoldenCase(t, ctx, tc)
		})
	}
}

func runOnlineVMGoldenCase(t *testing.T, ctx context.Context, tc onlineVMGoldenCase) {
	t.Helper()
	// 先补本地路由元数据，再设置用户空间。
	// HandlerQueryPromQL 内部会根据 metadata.GetUser(ctx).SpaceUID 决定从哪个空间路由表找 RT。
	addOnlineVMGoldenRouterFixture(t, ctx, tc)
	metadata.SetUser(ctx, &metadata.User{
		Key:      tc.QuerySource,
		SpaceUID: tc.SpaceUID,
		TenantID: tc.TenantID,
	})

	caseDir := filepath.Join(onlineVMGoldenCasesRoot, tc.Name)
	// reqCase 是真实入口请求；expect 是这条请求期望生成的下游 VM query_sync payload。
	reqCase := loadOnlineVMGoldenRequest(t, caseDir)
	expect := loadOnlineVMGoldenExpect(t, caseDir)
	require.Equal(t, http.MethodPost, reqCase.Method)
	require.Equal(t, http.MethodPost, expect.Method)
	require.Equal(t, "/query/ts/promql", reqCase.Path)
	require.Equal(t, "vm", expect.Body.PreferStorage)

	var captured onlineVMGoldenBKBaseRequest
	// 拦截发往 BKBase query_sync 的 HTTP 请求。
	// 测试不关心真实下游响应，只关心 UQ 实际构造出的 prefer_storage 和 sql。
	registerOnlineVMGoldenQuerySyncResponder(t, &captured)

	body, err := json.Marshal(reqCase.Body)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, reqCase.Method, reqCase.Path, bytes.NewReader(body))
	require.NoError(t, err)
	for k, v := range reqCase.Headers {
		req.Header.Set(k, v)
	}

	gin.SetMode(gin.TestMode)
	w := &Writer{}
	HandlerQueryPromQL(&gin.Context{
		Request: req,
		Writer:  w,
	})
	require.JSONEq(t, fmt.Sprintf(`{"series":[],"is_partial":false,"result_table_id":["%s"]}`, tc.WantResultTableID), w.body())

	// golden 对比点：
	// captured 来自 httpmock 截获到的真实下游请求；
	// expect 来自 expect.downstream.json。
	// 如果后续有人改了 VM 路由、RT 展开、filter 拼接或 query builder，
	// 导致最终下发的 query_sync sql 变化，这里会直接失败。
	require.Equal(t, expect.Body.PreferStorage, captured.PreferStorage)
	var actualSQL onlineVMGoldenQuerySQL
	require.NoError(t, json.Unmarshal([]byte(captured.SQL), &actualSQL))
	require.Equal(t, expect.Body.SQL, actualSQL)
}

func loadOnlineVMGoldenRequest(t *testing.T, caseDir string) onlineVMGoldenHTTPRequest {
	t.Helper()
	var req onlineVMGoldenHTTPRequest
	readOnlineVMGoldenJSON(t, caseDir, "request.json", &req)
	return req
}

func loadOnlineVMGoldenExpect(t *testing.T, caseDir string) onlineVMGoldenExpect {
	t.Helper()
	var expect onlineVMGoldenExpect
	readOnlineVMGoldenJSON(t, caseDir, "expect.downstream.json", &expect)
	return expect
}

func readOnlineVMGoldenJSON(t *testing.T, caseDir, name string, dst any) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(caseDir, name))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(content, dst))
}

func addOnlineVMGoldenRouterFixture(t *testing.T, ctx context.Context, tc onlineVMGoldenCase) {
	t.Helper()
	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err)

	space := router.GetSpace(ctx, tc.SpaceUID)
	if space == nil {
		space = make(ir.Space)
	}
	space[tc.TableID] = &ir.SpaceResultTable{
		TableId: tc.TableID,
	}
	require.NoError(t, router.Add(ctx, ir.SpaceToResultTableKey, tc.SpaceUID, &space))

	// ResultTableDetail 是 query builder 生成 VM expand 的关键来源。
	// BuildMetadataQuery 会用这里的 VmRt、Fields、MeasurementType 等信息，
	// 拼出 metric_filter_condition 和 result_table_list。
	require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, tc.TableID, &ir.ResultTableDetail{
		StorageId:       tc.StorageID,
		StorageName:     tc.StorageCluster,
		StorageType:     metadata.VictoriaMetricsStorageType,
		ClusterName:     tc.StorageCluster,
		TableId:         tc.TableID,
		Measurement:     tc.Measurement,
		VmRt:            tc.ResultTableID,
		Fields:          tc.Fields,
		MeasurementType: tc.MeasurementType,
		DataLabel:       tc.DataLabel,
	}))
}

func mockOnlineVMGoldenMustVMFeatureFlag(t *testing.T, ctx context.Context, cases []onlineVMGoldenCase) {
	t.Helper()
	// must-vm-query 决定这些 table_id 在本地测试里强制走 VM 直查路径。
	// 这里把 cases 中声明的 table_id 都加入 feature flag，避免新增 case 后还走到 InfluxDB/Prometheus mock 路径。
	tableIDs := []string{"result_table.vm", "result_table.k8s"}
	for _, tc := range cases {
		tableIDs = append(tableIDs, tc.TableID)
	}

	mustVMTableIDs, err := json.Marshal(tableIDs)
	require.NoError(t, err)
	mustVMQuery, err := json.Marshal(fmt.Sprintf("tableID in %s", string(mustVMTableIDs)))
	require.NoError(t, err)
	require.NoError(t, featureFlag.MockFeatureFlag(ctx, `{
		"bk-data-table-id-auth": {
			"variations": {
				"true": true,
				"false": false
			},
			"targeting": [{
				"query": "spaceUID in [\"bkdata\"]",
				"percentage": {
					"false": 100
				}
			}],
			"defaultRule": {
				"variation": "true"
			}
		},
		"jwt-auth": {
			"variations": {
				"true": true,
				"false": false
			},
			"targeting": [],
			"defaultRule": {
				"variation": "true"
			}
		},
		"must-vm-query": {
			"variations": {
				"true": true,
				"false": false
			},
			"targeting": [
				{
					"query": `+string(mustVMQuery)+`,
					"percentage": {
						"true": 100,
						"false": 0
					}
				},
				{
					"query": "tableID in [\"system.cpu_detail\", \"system.disk\"]",
					"percentage": {
						"true": 100,
						"false": 0
					}
				}
			],
			"defaultRule": {
				"variation": "false"
			}
		}
	}`))
}

func registerOnlineVMGoldenQuerySyncResponder(t *testing.T, captured *onlineVMGoldenBKBaseRequest) {
	t.Helper()
	httpmock.RegisterResponder(http.MethodPost, mock.BkBaseUrl, func(r *http.Request) (*http.Response, error) {
		// vmQuery 发给 BKBase 的 body 外层包含 prefer_storage 和 sql；
		// sql 本身是再次 JSON 编码后的 VM ParamsQueryRange。
		content, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(content, captured); err != nil {
			return nil, err
		}
		return httpmock.NewStringResponse(http.StatusOK, `{
			"result": true,
			"message": "OK",
			"code": "00",
			"data": {
				"list": [{
					"status": "success",
					"isPartial": false,
					"data": {
						"resultType": "matrix",
						"result": []
					}
				}]
			}
		}`), nil
	})
}
