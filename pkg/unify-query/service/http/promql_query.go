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
	"fmt"
	"io"

	"github.com/gin-gonic/gin"
	"github.com/golang/gddo/httputil/header"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// promqlReq
type promqlReq struct {
	PromQL              string   `json:"promql"`
	Start               string   `json:"start"`
	End                 string   `json:"end"`
	Step                string   `json:"step"`
	BKBizIDs            []string `json:"bk_biz_ids"`
	MaxSourceResolution string   `json:"max_source_resolution,omitempty"`
	NotAlignInfluxdb    bool     `json:"not_align_influxdb,omitempty"` // 不与influxdb对齐
	Limit               int      `json:"limit,omitempty"`
	Slimit              int      `json:"slimit,omitempty"`
	Match               string   `json:"match,omitempty"`
}

// HandleTsQueryPromQLToStructRequest PromQL 转结构化查询接口
func HandleTsQueryPromQLToStructRequest(c *gin.Context) {
	var (
		ctx = c.Request.Context()
		err error
	)

	// 这里开始context就使用trace生成的了
	ctx, span := trace.NewSpan(ctx, "handle-ts-promql-to-struct")
	defer span.End(&err)

	req := &promqlReq{}
	if err := json.NewDecoder(c.Request.Body).Decode(req); err != nil {
		// 统一返回解析body失败
		log.Warnf(context.TODO(), "read ts Unmarshal body failed for->[%s]", err)
		c.JSON(400, ErrResponse{Err: ErrReqAnalysis.Error()})
		return
	}

	sp := structured.NewStructParser(req.PromQL)
	qstruct, err := sp.ParseNew()
	if err != nil {
		// 这里分个类，promql解析失败
		c.JSON(400, ErrResponse{Err: ErrPromParse.Error()})
		return
	}

	c.JSON(200, gin.H{"data": qstruct})
}

// HandleTsQueryPromQLDataRequest 使用 PromQL 的方式查询时序数据
// 执行逻辑是先把 promql 转成结构体，再使用标准的结构体查询方案去查询
func HandleTsQueryPromQLDataRequest(c *gin.Context) {
	var (
		ctx = c.Request.Context()
		err error
	)

	// 这里开始context就使用trace生成的了
	ctx, span := trace.NewSpan(ctx, "handle-ts-query-promQL-data-request")
	defer span.End(&err)

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Errorf(context.TODO(), "read ts request body failed for->[%s]", err)
		c.JSON(400, ErrResponse{Err: err.Error()})
		return
	}
	// 如果header中有bk_biz_id，则以header中的值为最优先
	bizIDs := header.ParseList(c.Request.Header, BizHeader)
	spaceUid := c.Request.Header.Get(SpaceUIDHeader)

	span.Set("request-space-uid", spaceUid)
	span.Set("request-biz-ids", bizIDs)
	span.Set("promql-request-header", c.Request.Header)
	span.Set("promql-request-data", string(body))

	respData, err := handlePromqlQuery(ctx, string(body), bizIDs, spaceUid)
	if err != nil {
		log.Errorf(context.TODO(), "handle ts request failed for->[%s]", err)
		c.JSON(400, ErrResponse{Err: err.Error()})
		return
	}

	c.JSON(200, respData)
}

func handlePromqlQuery(ctx context.Context, promqlData string, bizIDs []string, spaceUid string) (*PromData, error) {
	var (
		req promqlReq
		err error
	)
	if err = json.Unmarshal([]byte(promqlData), &req); err != nil {
		return nil, err
	}

	ctx, span := trace.NewSpan(ctx, "handle-promql-query")
	defer span.End(&err)

	// 1. 仍然去解析ast，将metric_name 和conditions解析出来，并填充时间和分辨率，用来判断是否访问argus
	sp := structured.NewStructParser(req.PromQL)

	qstruct, err := sp.ParseNew()
	if err != nil {
		return nil, err
	}
	qstruct.Start = req.Start
	qstruct.End = req.End
	qstruct.Step = req.Step
	qstruct.MaxSourceResolution = req.MaxSourceResolution

	oldStmt := sp.String()
	span.Set("promQL", req.PromQL)
	span.Set("old-stmt", oldStmt)

	var reqPromql string

	// 3. 将metric_name 解析并填充到context中
	for _, q := range qstruct.QueryList {
		q.Start = qstruct.Start
		q.End = qstruct.End
		q.Step = qstruct.Step

		// 是否打开对齐，获取配置
		q.AlignInfluxdbResult = AlignInfluxdbResult

		queryInfo := new(promql.QueryInfo)
		// 传递将采样方法
		queryInfo.AggregateMethodList = make([]promql.AggrMethod, 0, len(q.AggregateMethodList))
		queryInfo.IsCount = false

		if len(q.AggregateMethodList) > 0 {
			// 传递将采样方法
			for ai, aggr := range q.AggregateMethodList {
				span.Set(fmt.Sprintf("aggregate-method-list-method-%d", ai), aggr.Method)
				span.Set(fmt.Sprintf("aggregate-method-list-dimensions-%d", ai), aggr.Dimensions)

				queryInfo.AggregateMethodList = append(queryInfo.AggregateMethodList, promql.AggrMethod{
					Name:       aggr.Method,
					Dimensions: aggr.Dimensions,
					Without:    aggr.Without,
				})
			}
			if q.TimeAggregation.Function == structured.CountOverTime && q.AggregateMethodList[0].Method == "sum" {
				queryInfo.IsCount = true
				q.TimeAggregation.Function = structured.SumOverTime
			}
		}

		if req.Limit > 0 {
			queryInfo.OffsetInfo.Limit = req.Limit
		}
		if req.Slimit > 0 {
			queryInfo.OffsetInfo.SLimit = req.Slimit
		}

		if spaceUid != "" {
			tsDBs, err1 := structured.GetTsDBList(ctx, &structured.TsDBOption{
				SpaceUid:  spaceUid,
				TableID:   q.TableID,
				FieldName: string(q.FieldName),
			})
			if err1 != nil {
				return nil, err1
			}
			queryInfo.TsDBs = tsDBs
			ctx, err1 = promql.QueryInfoIntoContext(ctx, string(q.ReferenceName), string(q.FieldName), queryInfo)
			if err1 != nil {
				return nil, err1
			}

			span.Set("query-info-spaceUid", spaceUid)
			span.Set("query-nfo-tsdb", tsDBs.StringSlice())
			span.Set("query-info-measurement", queryInfo.Measurement)
			span.Set("query-info-clusterID", queryInfo.ClusterID)
			continue
		}

		structured.ReplaceOrAddCondition(&q.Conditions, structured.BizID, bizIDs)
		tableIDFilter, err1 := structured.NewTableIDFilter(string(q.FieldName), q.TableID, nil, q.Conditions)
		if err1 != nil {
			return nil, err1
		}

		if !tableIDFilter.IsAppointTableID() {
			queryInfo.DataIDList = tableIDFilter.DataIDList()
		} else {
			routes := tableIDFilter.GetRoutes()
			// 指定tableid的情况下，长度必定为1
			for _, route := range routes {
				queryInfo.DB = route.DB()
				queryInfo.Measurement = route.Measurement()
				queryInfo.ClusterID = route.ClusterID()
			}
		}

		queryInfo.IsPivotTable = influxdb.IsPivotTable(string(q.TableID))
		if len(req.BKBizIDs) > 0 {
			queryInfo.Conditions = [][]promql.ConditionField{
				{
					{
						DimensionName: structured.BizID,
						Operator:      "=",
						Value:         req.BKBizIDs,
					},
				},
			}
		}

		span.Set("query-info-is-count", queryInfo.IsCount)
		span.Set("query-info-db", queryInfo.DB)
		span.Set("query-info-measurement", queryInfo.Measurement)
		span.Set("query-info-clusterID", queryInfo.ClusterID)

		ctx, err1 = promql.QueryInfoIntoContext(ctx, string(q.ReferenceName), string(q.FieldName), queryInfo)
		if err1 != nil {
			return nil, err1
		}
	}

	if req.Match != "" {
		matchers, err := parser.ParseMetricSelector(req.Match)
		if err != nil {
			return nil, err
		}

		if len(matchers) > 0 {
			for _, m := range matchers {
				for _, q := range qstruct.QueryList {
					q.Conditions.Append(structured.ConditionField{
						DimensionName: m.Name,
						Value:         []string{m.Value},
						Operator:      structured.PromOperatorToConditions(m.Type),
					}, structured.ConditionAnd)
				}
			}
		}
	}

	promExpr, err := qstruct.ToProm(ctx, &structured.Option{
		IsOnlyParse:     true,
		SpaceUid:        spaceUid,
		IsAlignInfluxdb: true,
	})
	if err != nil {
		return nil, err
	}
	reqPromql = promExpr.GetExpr().String()
	span.Set("new-stmt", reqPromql)
	return HandleRawPromQuery(ctx, reqPromql, &qstruct)
}
