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
	"encoding/json"
	"io"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/es"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// ErrResponse
type ErrResponse struct {
	Err string `json:"error"`
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
		ctx         = c.Request.Context()
		span        oleltrace.Span
		user        = metadata.GetUser(ctx)
		servicePath = c.Request.URL.Path
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "handle-es-request")
	if span != nil {
		defer span.End()
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Errorf(context.TODO(), "read es request body failed for->[%s]", err)
		metric.APIRequestInc(ctx, servicePath, metric.StatusFailed, user.SpaceUid)
		c.JSON(400, ErrResponse{err.Error()})
		return
	}
	var req *ESRequest
	err = json.Unmarshal(body, &req)
	if err != nil {
		log.Errorf(context.TODO(), "anaylize es request body failed for->[%s]", err)
		metric.APIRequestInc(ctx, servicePath, metric.StatusFailed, user.SpaceUid)
		c.JSON(400, ErrResponse{err.Error()})
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
		log.Errorf(context.TODO(), "query es failed for->[%s]", err)
		metric.APIRequestInc(ctx, servicePath, metric.StatusFailed, user.SpaceUid)
		c.JSON(400, ErrResponse{err.Error()})
		return
	}

	metric.APIRequestInc(ctx, servicePath, metric.StatusSuccess, user.SpaceUid)
	c.String(200, "%s", result)
}

// registerESService
func registerESService(g *gin.Engine) {
	servicePath := viper.GetString(ESHandlePathConfigPath)
	g.POST(servicePath, HandleESQueryRequest)
	log.Infof(context.TODO(), "es service register in path->[%s]", servicePath)
}
