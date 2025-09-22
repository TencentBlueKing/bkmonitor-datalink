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
	"context"
	"io"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errno"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/es"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// ErrResponse
type ErrResponse struct {
	TraceID string `json:"trace_id,omitempty"`
	Err     string `json:"error"`
}

// ESRequest
type ESRequest struct {
	TableID string `json:"table_id"`
	Time    *Time  `json:"time"`
	Query   *Query `json:"query"`
}

// Time
type Time struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// Query
type Query struct {
	Body          string `json:"body"`
	FuzzyMatching bool   `json:"fuzzy_matching"`
}

// 处理请求
func HandleESQueryRequest(c *gin.Context) {
	// 这里开始context就使用trace生成的了
	var (
		ctx = c.Request.Context()

		user        = metadata.GetUser(ctx)
		servicePath = c.Request.URL.Path

		err error
	)

	ctx, span := trace.NewSpan(ctx, "handle-es-request")
	defer span.End(&err)

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		codedErr := errno.ErrDataProcessFailed().
			WithComponent("HTTP").
			WithOperation("ES请求体读取").
			WithError(err).
			WithSolution("检查请求体大小限制和内容格式")
		log.ErrorWithCodef(context.TODO(), codedErr)
		metric.APIRequestInc(ctx, servicePath, metric.StatusFailed, user.SpaceUID, user.Source)
		c.JSON(400, ErrResponse{Err: err.Error()})
		return
	}
	var req *ESRequest
	err = json.Unmarshal(body, &req)
	if err != nil {
		codedErr := errno.ErrDataFormatInvalid().
			WithComponent("HTTP").
			WithOperation("ES查询解析").
			WithError(err).
			WithSolution("检查JSON格式和Elasticsearch查询语法正确性")
		log.ErrorWithCodef(context.TODO(), codedErr)
		metric.APIRequestInc(ctx, servicePath, metric.StatusFailed, user.SpaceUID, user.Source)
		c.JSON(400, ErrResponse{Err: err.Error()})
		return
	}
	params := &es.Params{
		TableID:       req.TableID,
		Start:         req.Time.Start,
		End:           req.Time.End,
		Body:          req.Query.Body,
		FuzzyMatching: req.Query.FuzzyMatching,
	}
	result, err := es.Query(params)
	if err != nil {
		codedErr := errno.ErrBusinessLogicError().
			WithComponent("Elasticsearch").
			WithOperation("执行查询").
			WithError(err).
			WithSolution("检查ES集群状态、索引存在性和查询参数")
		log.ErrorWithCodef(context.TODO(), codedErr)
		metric.APIRequestInc(ctx, servicePath, metric.StatusFailed, user.SpaceUID, user.Source)
		c.JSON(400, ErrResponse{Err: err.Error()})
		return
	}

	metric.APIRequestInc(ctx, servicePath, metric.StatusSuccess, user.SpaceUID, user.Source)
	c.String(200, "%s", result)
}
