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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const (
	BKBaseSurrealDBUrlConfigPath           = "cmdb.v1beta3.surrealdb.bkbase.url"
	BKBaseSurrealDBResultTableIDConfigPath = "cmdb.v1beta3.surrealdb.bkbase.result_table_id"
	BKBaseSurrealDBPreferStorageConfigPath = "cmdb.v1beta3.surrealdb.bkbase.prefer_storage"
	BKBaseSurrealDBAuthMethodConfigPath    = "cmdb.v1beta3.surrealdb.bkbase.auth_method"
	BKBaseSurrealDBUsernameConfigPath      = "cmdb.v1beta3.surrealdb.bkbase.username"
	BKBaseSurrealDBAppCodeConfigPath       = "cmdb.v1beta3.surrealdb.bkbase.app_code"
	BKBaseSurrealDBAppSecretConfigPath     = "cmdb.v1beta3.surrealdb.bkbase.app_secret"
	BKBaseSurrealDBTimeoutConfigPath       = "cmdb.v1beta3.surrealdb.bkbase.timeout"
)

var (
	DefaultBKBaseSurrealDBUrl           = "path-to-surrealDB"
	DefaultBKBaseSurrealDBResultTableID = ""
	DefaultBKBaseSurrealDBPreferStorage = "surrealdb"
	DefaultBKBaseSurrealDBAuthMethod    = "user"
	DefaultBKBaseSurrealDBUsername      = ""
	DefaultBKBaseSurrealDBAppCode       = ""
	DefaultBKBaseSurrealDBAppSecret     = ""
	DefaultBKBaseSurrealDBTimeout       = 30 * time.Second
)

var (
	BKBaseSurrealDBUrl           string
	BKBaseSurrealDBResultTableID string
	BKBaseSurrealDBPreferStorage string
	BKBaseSurrealDBAuthMethod    string
	BKBaseSurrealDBUsername      string
	BKBaseSurrealDBAppCode       string
	BKBaseSurrealDBAppSecret     string
	BKBaseSurrealDBTimeout       time.Duration
)

type BKBaseSurrealDBConfig struct {
	Url           string
	ResultTableID string
	PreferStorage string
	AuthMethod    string
	Username      string
	AppCode       string
	AppSecret     string
	Timeout       time.Duration
}

type BKBaseSurrealDBClient struct {
	config BKBaseSurrealDBConfig
	curl   curl.Curl
}

type BKBaseRequest struct {
	SQL                        string `json:"sql"`
	BKDataAuthenticationMethod string `json:"bkdata_authentication_method"`
	PreferStorage              string `json:"prefer_storage"`
	BKUsername                 string `json:"bk_username"`
	BKAppCode                  string `json:"bk_app_code"`
	BKAppSecret                string `json:"bk_app_secret"`
}

type BKBaseSQLPayload struct {
	DSL           string `json:"dsl"`
	ResultTableID string `json:"result_table_id"`
}

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
	return NewBKBaseSurrealDBClientWithConfig(BKBaseSurrealDBConfig{
		Url:           BKBaseSurrealDBUrl,
		ResultTableID: BKBaseSurrealDBResultTableID,
		PreferStorage: BKBaseSurrealDBPreferStorage,
		AuthMethod:    BKBaseSurrealDBAuthMethod,
		Username:      BKBaseSurrealDBUsername,
		AppCode:       BKBaseSurrealDBAppCode,
		AppSecret:     BKBaseSurrealDBAppSecret,
		Timeout:       BKBaseSurrealDBTimeout,
	})
}

func NewBKBaseSurrealDBClientWithConfig(config BKBaseSurrealDBConfig) *BKBaseSurrealDBClient {
	return &BKBaseSurrealDBClient{
		config: config,
		curl:   &curl.HttpCurl{},
	}
}

func (c *BKBaseSurrealDBClient) Execute(ctx context.Context, sql string, start, end int64) ([]*LivenessGraph, error) {
	var err error
	ctx, span := trace.NewSpan(ctx, "bkbase-surrealdb-execute")
	defer span.End(&err)

	span.Set("sql", sql)
	span.Set("start", start)
	span.Set("end", end)
	span.Set("result_table_id", c.config.ResultTableID)

	sqlPayload := BKBaseSQLPayload{
		DSL:           sql,
		ResultTableID: c.config.ResultTableID,
	}
	sqlPayloadBytes, err := json.Marshal(sqlPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal sql payload: %w", err)
	}

	request := BKBaseRequest{
		SQL:                        string(sqlPayloadBytes),
		BKDataAuthenticationMethod: c.config.AuthMethod,
		PreferStorage:              c.config.PreferStorage,
		BKUsername:                 c.config.Username,
		BKAppCode:                  c.config.AppCode,
		BKAppSecret:                c.config.AppSecret,
	}

	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	span.Set("request_url", c.config.Url)

	var resp BKBaseResponse
	_, err = c.curl.Request(ctx, curl.Post, curl.Options{
		UrlPath: c.config.Url,
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		Body:    requestBody,
		Timeout: c.config.Timeout,
	}, &resp)
	if err != nil {
		return nil, fmt.Errorf("bkbase request failed: %w", err)
	}

	if !resp.Result {
		return nil, fmt.Errorf("bkbase response error: code=%s, message=%s", resp.Code, resp.Message)
	}

	span.Set("trace_id", resp.TraceID)
	span.Set("total_records", resp.Data.TotalRecords)

	// 转换响应格式为标准 SurrealDB 响应格式
	// BKBase 返回格式: {"data": {"list": [...]}}
	// 标准格式: [{"result": [...]}]
	rawResponse := []map[string]any{
		{
			ResponseFieldResult: resp.Data.List,
		},
	}

	parser := NewSurrealResponseParser(start, end)
	return parser.Parse(rawResponse)
}

func (c *BKBaseSurrealDBClient) SetResultTableID(resultTableID string) {
	c.config.ResultTableID = resultTableID
}

func (c *BKBaseSurrealDBClient) GetResultTableID() string {
	return c.config.ResultTableID
}
