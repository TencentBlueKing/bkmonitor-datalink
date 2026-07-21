// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/bkapi"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// 保留的 v1beta3 专属配置
const (
	BKBaseSurrealDBResultTableIDConfigPath = "cmdb.v1beta3.surrealdb.bkbase.result_table_id"
	BKBaseSurrealDBTimeoutConfigPath       = "cmdb.v1beta3.surrealdb.bkbase.timeout"
	BKBaseSurrealDBQueryURLConfigPath      = "cmdb.v1beta3.surrealdb.bkbase.query_url"
	BindingCacheTTLConfigPath              = "cmdb.v1beta3.binding_cache_ttl"
	BindingCacheMaxSizeConfigPath          = "cmdb.v1beta3.binding_cache_max_size"
	BindingRedisKeyConfigPath              = "cmdb.v1beta3.binding_redis_key"

	// PreferStorageSurrealDB 是 bkbase query_sync 的 prefer_storage 固定值
	PreferStorageSurrealDB = "surrealdb"
)

var (
	// DefaultBKBaseSurrealDBResultTableID 在没有 binding 时可做 fallback（通常留空）
	DefaultBKBaseSurrealDBResultTableID = ""
	DefaultBKBaseSurrealDBTimeout       = 30 * time.Second
	DefaultBindingCacheTTL              = 5 * time.Minute
	DefaultBindingCacheMaxSize          = 10000
	DefaultBindingRedisKey              = "bkmonitorv3:spaces:surrealdb_binding"
)

var (
	BKBaseSurrealDBResultTableID string
	BKBaseSurrealDBTimeout       time.Duration
	BKBaseSurrealDBQueryURL      string
	BindingCacheTTL              time.Duration
	BindingCacheMaxSize          int
	BindingRedisKey              string
)

// BKBaseSurrealDBClient 通过 bkbase query_sync 接口转发 SurrealQL 查询。
//
// 凭据 / URL 统一走 bkapi.GetBkDataAPI()（与 ES / BKSQL / VictoriaMetrics 一致）。
// 每次 Execute 接收 resultTableID / namespace / database 参数，由调用方从
// SurrealDBBinding 的 metadata.annotations 解析而来（见 binding_resolver）。
type BKBaseSurrealDBClient struct {
	timeout time.Duration
	curl    curl.Curl
}

// BKBaseSQLPayload 是塞进 body.sql 字段的 JSON 字符串（bkbase 协议要求）。
type BKBaseSQLPayload struct {
	DSL           string `json:"dsl"`
	ResultTableID string `json:"result_table_id"`
}

type BKBaseQuerySyncProperties struct {
	ClusterName string `json:"cluster_name,omitempty"`
}

// BKBaseResponse 是 bkbase query_sync 的响应壳子。
type BKBaseResponse struct {
	Result  bool        `json:"result"`
	Code    string      `json:"code"`
	Data    *BKBaseData `json:"data"`
	Message string      `json:"message"`
	Errors  any         `json:"errors"`
	TraceID string      `json:"trace_id"`
}

type BKBaseData struct {
	TotalRecords      int              `json:"total_records"`
	Device            string           `json:"device"`
	Cluster           string           `json:"cluster"`
	ResultTableIDs    []string         `json:"result_table_ids"`
	List              []map[string]any `json:"list"`
	SelectFieldsOrder []string         `json:"select_fields_order"`
	Timetaken         float64          `json:"timetaken"`
}

func NewBKBaseSurrealDBClient() *BKBaseSurrealDBClient {
	timeout := BKBaseSurrealDBTimeout
	if timeout <= 0 {
		timeout = DefaultBKBaseSurrealDBTimeout
	}
	return &BKBaseSurrealDBClient{
		timeout: timeout,
		curl:    &curl.HttpCurl{},
	}
}

// Execute 通过 bkbase query_sync 接口转发 SurrealQL 查询。
//
// 参数：
//
//	spaceUID   —— 用于选 bkbase 多集群路由（bk_data.cluster_space_uid）
//	rtID       —— 绑定对应的 bkbase result_table_id（= binding.annotations.database）
//	namespace  —— SurrealDB namespace（= binding.annotations.namespace，如 "mapleleaf_39"）
//	database   —— SurrealDB database（与 rtID 相同，单独传显式一点）
//	dsl        —— 真正的 SurrealQL；内部会加 "USE NS ... DB ...;" 前缀
//
// 注：本函数实现 GraphQueryExecutor 接口（v1beta3.go 定义）。当 spaceUID / rtID /
// namespace / database 都为空时退化到全局配置 + 无 USE NS 前缀的行为，便于
// 旧代码路径（如单测）继续工作。
func (c *BKBaseSurrealDBClient) Execute(ctx context.Context, sql string, start, end int64) ([]*LivenessGraph, error) {
	return c.ExecuteWithBinding(ctx, "", BindingInfo{}, sql, start, end)
}

// ExecuteWithBinding 带 binding 上下文执行查询。
//
// 这是新的首选接口 —— 由 Model 在拿到 binding 元信息后调用。
// spaceUID 仅用于 bkbase 多集群 URL 路由；和 binding.SpaceUID 不强相关。
func (c *BKBaseSurrealDBClient) ExecuteWithBinding(ctx context.Context, spaceUID string, binding BindingInfo, dsl string, start, end int64) (graphs []*LivenessGraph, err error) {
	ctx, span := trace.NewSpan(ctx, "bkbase-surrealdb-execute")
	defer endV1Beta3TraceSpan(span, &err)

	span.Set("space-uid", spaceUID)
	span.Set("namespace", binding.Namespace)
	span.Set("cluster-name", binding.ClusterName)
	span.Set("binding-enabled", binding.Namespace != "" || binding.Database != "")
	span.Set("start", start)
	span.Set("end", end)

	rtID := binding.Database
	if rtID == "" {
		rtID = BKBaseSurrealDBResultTableID
	}
	span.Set("result-table-id", rtID)

	finalDSL := dsl
	if (binding.Namespace == "") != (binding.Database == "") {
		return nil, fmt.Errorf("binding namespace and database must either both be set or both be empty")
	}
	if binding.Namespace != "" {
		if err := validateBindingIdentifier("namespace", binding.Namespace); err != nil {
			return nil, err
		}
		if err := validateBindingIdentifier("database", binding.Database); err != nil {
			return nil, err
		}
		// BKBase query_sync 只负责把 DSL 发送到 SurrealDB；具体 NS/DB 必须由 UQ 根据
		// SurrealDBBinding 注入，否则同一个 result_table_id 在多租户场景下会查到错误 database。
		finalDSL = fmt.Sprintf("USE NS `%s` DB `%s`;%s", binding.Namespace, binding.Database, dsl)
	}
	span.Set("dsl", finalDSL)
	span.Set("dsl-bytes", len(finalDSL))

	sqlPayload := BKBaseSQLPayload{
		DSL:           finalDSL,
		ResultTableID: rtID,
	}
	sqlPayloadBytes, err := json.Marshal(sqlPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal sql payload: %w", err)
	}
	span.Set("sql-payload-bytes", len(sqlPayloadBytes))

	dataAPI := bkapi.GetBkDataAPI()

	reqMap := map[string]any{
		"sql":            string(sqlPayloadBytes),
		"prefer_storage": PreferStorageSurrealDB,
	}
	if binding.ClusterName != "" {
		// route 里带 cluster_name 时显式传给 BKBase，避免 query_sync 只靠 result_table_id
		// 在多集群环境中选到默认 SurrealDB 集群。
		reqMap["properties"] = BKBaseQuerySyncProperties{ClusterName: binding.ClusterName}
	}
	for k, v := range dataAPI.GetDataAuth() {
		reqMap[k] = v
	}
	requestBody, err := json.Marshal(reqMap)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	span.Set("request-body-bytes", len(requestBody))

	url := surrealDBQuerySyncURL(spaceUID)
	span.Set("request-url", url)

	var resp BKBaseResponse
	_, err = c.curl.Request(ctx, curl.Post, curl.Options{
		UrlPath: url,
		Headers: metadata.Headers(ctx, dataAPI.Headers(map[string]string{"Content-Type": "application/json"})),
		Body:    requestBody,
		Timeout: c.timeout,
	}, &resp)
	if err != nil {
		return nil, fmt.Errorf("bkbase request failed: %w", err)
	}

	span.Set("bkbase-result", resp.Result)
	span.Set("bkbase-code", resp.Code)
	span.Set("trace-id", resp.TraceID)
	if !resp.Result {
		return nil, fmt.Errorf("bkbase response error: code=%s, message=%s", resp.Code, resp.Message)
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("parse bkbase response: result=true requires non-null data")
	}

	span.Set("total-records", resp.Data.TotalRecords)
	span.Set("bkbase-timetaken", resp.Data.Timetaken)
	span.Set("response-list-count", len(resp.Data.List))

	// 转换响应格式为标准 SurrealDB 响应格式
	// BKBase 返回格式: {"data": {"list": [...]}}
	// 标准格式: [{"result": [...]}]
	list := make([]any, 0, len(resp.Data.List))
	for _, item := range resp.Data.List {
		list = append(list, item)
	}
	// parser 只依赖标准 SurrealDB 客户端形态：[{"result": [...]}]。
	// BKBase query_sync 的 data.list 在这里包一层 result，可以让解析器和单测 mock 共用同一套结构。
	rawResponse := []map[string]any{
		{
			ResponseFieldResult: list,
		},
	}

	parser := NewSurrealResponseParser(start, end)
	graphs, err = parser.Parse(rawResponse)
	if err != nil {
		return nil, fmt.Errorf("parse surrealdb response: %w", err)
	}
	span.Set("graph-count", len(graphs))
	return graphs, nil
}

func validateBindingIdentifier(kind, value string) error {
	if value == "" {
		return fmt.Errorf("binding %s cannot be empty", kind)
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return fmt.Errorf("binding %s %q contains invalid identifier character %q", kind, value, r)
	}
	return nil
}

func surrealDBQuerySyncURL(spaceUID string) string {
	if BKBaseSurrealDBQueryURL != "" {
		return BKBaseSurrealDBQueryURL
	}
	return bkapi.GetBkDataAPI().QueryUrl(spaceUID)
}
