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
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	headerutil "github.com/golang/gddo/httputil/header"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/infos"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// LabelValueOptions
type LabelValueOptions struct {
	LabelName string
	Start     time.Duration
	End       time.Duration
	Matches   string
	TableID   string
}

// HandleLabelValuesRequest: 模拟普罗label_values接口
// 不支持__name__等tag
// 功能相当于influxdb show tag keys
func HandleLabelValuesRequest(c *gin.Context) {
	var (
		ctx = c.Request.Context()
		err error
	)

	ctx, span := trace.NewSpan(ctx, "handle-ts-request")
	defer span.End(&err)

	labelName := c.Param("label_name")
	// start=<rfc3339 | unix_timestamp>: Start timestamp. Optional.
	// end=<rfc3339 | unix_timestamp>: End timestamp. Optional.
	// match[]=<series_selector>:
	// Repeated series selector argument that selects the series from which to read the label values. Optional.
	tableID, _ := c.Params.Get("table_id") // Optional.
	matches := c.QueryArray("match[]")

	// 如果header中有bkbizid，则以header中的值为最优先
	bizIDs := headerutil.ParseList(c.Request.Header, BizHeader)
	spaceUid := c.Request.Header.Get(SpaceUIDHeader)

	paramsStr := fmt.Sprintf("name:%s, table_id:%s, match[]:%v", labelName, tableID, matches)

	span.Set("request-space-uid", spaceUid)
	span.Set("request-biz-ids", bizIDs)
	span.Set("info-request-header", fmt.Sprintf("%+v", c.Request.Header))
	span.Set("request-data", paramsStr)

	log.Debugf(ctx, "recevice query info: %s, X-Bk-Scope-Biz-Id:%v ", paramsStr, bizIDs)

	if !model.LabelNameRE.MatchString(labelName) {
		log.Errorf(ctx, "bad label name: %s", labelName)
		c.JSON(400, ErrResponse{Err: ErrInvalidLabelName.Error()})
		return
	}

	// label_name 对 __name__ 做特殊处理：相当于show field keys
	// 由于容器查询存储为 单指标单表，这里如果匹配到容器查询方式，则会直接变成返回参数中的指标，无意义。所以将__name__等内置指标直接返回空
	if labelName == promql.MetricLabelName {
		log.Debugf(ctx, "%s will be return emtpy soon", promql.MetricLabelName)
		c.JSON(200, []interface{}{})
	}

	infoType := infos.TagValues
	params := &infos.Params{
		TableID:    structured.TableID(tableID),
		Conditions: structured.Conditions{},
		Keys:       []string{labelName},
		Limit:      100, // 默认为100
	}

	// Optional.
	if limit, hasLimit := c.Params.Get("limit"); hasLimit {
		intLimit, err := strconv.Atoi(limit)
		if err != nil {
			log.Warnf(context.TODO(), "parse limit err:%s", err)
		} else {
			params.Limit = intLimit
		}
	}

	// 1. to labelMatcher
	var mlist [][]*labels.Matcher
	if len(matches) != 0 {
		mlist = make([][]*labels.Matcher, 0, len(matches))
		for _, match := range matches {
			m, err := parser.ParseMetricSelector(match)
			if err != nil {
				log.Errorf(context.TODO(), "error match[]: %s", match)
				c.JSON(400, ErrResponse{Err: ErrPromParse.Error()})
				return
			}
			mlist = append(mlist, m)
		}
	}

	if len(mlist) == 0 {
		c.JSON(400, ErrResponse{Err: "match[] is needed"})
		return
	}

	// match[] 为 一个二维数组，数组之间是或的关系
	var results = influxdb.NewTables()
	for _, m := range mlist {
		metric, fields, err := structured.LabelMatcherToConditions(m)
		if err != nil {
			c.JSON(400, ErrResponse{Err: ErrLabelMatcher.Error()})
			return
		}

		if metric == "" {
			c.JSON(400, ErrResponse{Err: "empty metric"})
			return
		}
		// 给Conditions 加上and
		for i := 0; i < len(fields)-1; i++ {
			params.Conditions.ConditionList = append(params.Conditions.ConditionList, structured.ConditionAnd)
		}

		route, err := structured.MakeRouteFromMetricName(metric)
		if err != nil {
			c.JSON(400, ErrResponse{Err: err.Error()})
			return
		}
		params.Metric = route.MetricName()
		params.TableID = route.TableID()
		params.Conditions.FieldList = fields
		structured.ReplaceOrAddCondition(&params.Conditions, structured.BizID, bizIDs)

		// 3. 查询数据
		result, err := infos.QueryAsync(ctx, infoType, params, spaceUid)
		if err != nil {
			c.JSON(400, ErrResponse{Err: err.Error()})
			return
		}
		results.Add(result.Tables...)
	}

	// 这里只会查show tag values
	data, err := convertInfoData(ctx, infoType, params, results)
	if err != nil {
		c.JSON(400, ErrResponse{Err: err.Error()})
		return
	}
	c.JSON(200, data)
}
