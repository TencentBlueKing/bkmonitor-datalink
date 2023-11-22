// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
)

// CombinedQueryParams
type CombinedQueryParams struct {
	// QueryList 查询实例
	QueryList []*QueryParams `json:"query_list"`
	// MetricMerge 表达式：支持所有PromQL语法
	MetricMerge MetricMerge `json:"metric_merge" example:"a"`

	// OrderBy 弃用字段
	OrderBy OrderBy `json:"order_by" swaggerignore:"true"`
	// ResultColumns 指定保留返回字段值
	ResultColumns []string `json:"result_columns" swaggerignore:"true"`

	// Start 开始时间：单位为毫秒的时间戳
	Start string `json:"start_time" example:"1657848000"`
	// End 结束时间：单位为毫秒的时间戳
	End string `json:"end_time" example:"1657851600"`
	// Step 步长：最终返回的点数的时间间隔
	Step string `json:"step" example:"1m"`
	// DownSampleRange 降采样：大于Step才能生效，可以为空
	DownSampleRange string `json:"down_sample_range,omitempty" example:"5m"`
	// MaxSourceResolution 弃用字段，原 argus 场景
	MaxSourceResolution string `json:"max_source_resolution,omitempty" swaggerignore:"true"`
}

const (
	bkDatabaseLabelName    = "bk_database"    // argus/prom 存储, db 名对应的 label 名称
	bkMeasurementLabelName = "bk_measurement" // argus/prom 存储, 表名对应的 label 名称

	BkMonitor = "bkmonitor"
	Custom    = "custom"
	BkData    = "bkdata"
	BkLog     = "bklog"
	BkApm     = "bkapm"
)

var dataSourceMap = map[string]struct{}{
	BkMonitor: {},
	Custom:    {},
	BkData:    {},
	BkLog:     {},
	BkApm:     {},
}

// ToProm 结构化数据 -> promql -> 判断查询
func (q *CombinedQueryParams) ToProm(ctx context.Context, options *Option) (*PromExpr, error) {
	var (
		err  error
		expr *PromExpr

		exprMap = make(map[string]*PromExpr)
		result  = new(PromExpr)
	)

	if q.MetricMerge == "" {
		return nil, fmt.Errorf("metric merge is empty")
	}

	// 先将整个计算表达式用promql进行解析:
	if result.Expr, err = parser.ParseExpr(string(q.MetricMerge)); err != nil {
		log.Errorf(context.TODO(), "failed to parser metric_merge->[%s] for err->[%s]", string(q.MetricMerge), err)
		return nil, err
	}

	// 获取所有指标查询的表达式
	for _, query := range q.QueryList {
		if expr, err = query.ToProm(ctx, options); err != nil {
			log.Warnf(context.TODO(), "failed to translate metric->[%s] for->[%s]", query.FieldName, err)
			return nil, err
		}
		// 保存，并更新context
		exprMap[string(query.ReferenceName)] = expr
		ctx = expr.ctx
		log.Debugf(context.TODO(), "field->[%s] reference_name->[%s] add to map.", query.FieldName, query.ReferenceName)
	}

	result.Expr, err = HandleExpr(exprMap, result.Expr)

	if err != nil {
		return nil, err
	}

	// 无论如何，做一次ctx的替换
	result.ctx = ctx

	return result, nil
}

// QueryParams 查询结构
type QueryParams struct {
	// DataSource 暂不使用
	DataSource string `json:"data_source" swaggerignore:"true"`
	// DB 弃用字段
	DB Bucket `json:"db" swaggerignore:"true"`
	// TableID 数据实体ID，容器指标可以为空
	TableID TableID `json:"table_id" example:"system.cpu_summary"`
	// DataIDList 保留字段，无需使用
	DataIDList []consul.DataID `json:"-" swaggerignore:"true"` // 解析时会被上层结构化查询的dataID列表覆盖
	// IsFreeSchema 暂不使用
	IsFreeSchema bool `json:"is_free_schema" swaggerignore:"true"`
	// FieldName 查询指标
	FieldName FieldName `json:"field_name" example:"usage"`
	// FieldList 仅供 exemplar 查询 trace 指标时使用
	FieldList []FieldName `json:"field_list" example:"" swaggerignore:"true"` // 目前是供查询trace指标列时，可以批量查询使用
	// AggregateMethodList 维度聚合函数
	AggregateMethodList []AggregateMethod `json:"function"`
	// TimeAggregation 时间聚合方法
	TimeAggregation TimeAggregation `json:"time_aggregation"`
	// ReferenceName 别名，用于表达式计算
	ReferenceName ReferenceName `json:"reference_name" example:"a"`
	// Dimensions promQL 使用维度
	Dimensions Dimensions `json:"dimensions" example:"bk_target_ip,bk_target_cloud_id"`
	// Driver 弃用字段
	Driver string `json:"driver" example:"influxdb" swaggerignore:"true"`
	// TimeField 弃用字段
	TimeField string `json:"time_field" example:"time" swaggerignore:"true"`
	// Window 弃用字段
	Window Window `json:"window" example:"" swaggerignore:"true"`
	// Limit 点数限制数量
	Limit Limit `json:"limit" example:"0"`

	// Timestamp @-modifier 标记
	Timestamp *int64 `json:"timestamp"`
	// StartOrEnd @-modifier 标记，start or end
	StartOrEnd parser.ItemType `json:"start_or_end"`
	// VectorOffset
	VectorOffset time.Duration `json:"vector_offset"`

	// Offset 偏移量
	Offset string `json:"offset" example:""`
	// OffsetForward 偏移方向，默认 false 为向前偏移
	OffsetForward bool `json:"offset_forward" example:"false"`
	// Slimit 维度限制数量
	Slimit int `json:"slimit" example:"0"`
	// Soffset 弃用字段
	Soffset int `json:"soffset" example:"0" swaggerignore:"true"`
	// Conditions 过滤条件
	Conditions Conditions `json:"conditions"`
	// NotCombineWindow 弃用字段
	NotCombineWindow bool `json:"not_combine_window" swaggerignore:"true"`
	// KeepColumns 保留字段
	KeepColumns KeepColumns `json:"keep_columns" swaggerignore:"true"`
	// Start 弃用字段，会被外面的 Start 覆盖
	Start string `json:"start_time" swaggerignore:"true"`
	// End 弃用字段，会被外面的 End 覆盖
	End string `json:"end_time" swaggerignore:"true"`
	// Step 弃用字段，会被外面的 Step 覆盖
	Step string `json:"-" swaggerignore:"true"`
	// OrderBy 弃用字段
	OrderBy OrderBy `json:"order_by" swaggerignore:"true"`

	// AlignInfluxdbResult 保留字段，无需配置，是否对齐influxdb的结果,该判断基于promql和influxdb查询原理的差异
	AlignInfluxdbResult bool `json:"-"`
}

func (q *QueryParams) checkOption(ctx context.Context, opt *Option) (string, string, string, string, error) {

	var (
		err      error
		tableErr error
		route    *Route

		clusterID   string
		db          string
		measurement string
		metricName  string
	)
	// 入参可以分为两种形式： 一种是指定了tableID，一种是未指定tableID
	// 这里优先从TableID中获取库表
	route, tableErr = MakeRouteFromTableID(q.TableID)
	// 查询的几种场景
	// 1. 含有tableID，必定以tableID为最优先
	// 2. 没有tableID
	//    必须提供bk_biz_id，bcs_cluster_id/project_id可选，过滤该业务下存在该指标的data_id列表

	if route != nil {
		clusterID = route.ClusterID()
		db = route.DB()
		measurement = route.Measurement()
	}

	// 是否取指标原名
	if opt.IsRealFieldName {
		if tableErr == nil {
			route.SetMetricName(string(q.FieldName))
			metricName = route.RealMetricName()
		} else if q.DataSource != "" {
			metricName = strings.Join([]string{q.DataSource, string(q.FieldName)}, ":")
		} else {
			metricName = string(q.FieldName)
		}
	} else {
		metricName = string(q.ReferenceName)
	}

	// 需要访问argus或者仅仅转化时，防止labelList中缺少，将其转化为特殊正则
	if opt.IsOnlyParse {
		for i, cond := range q.Conditions.FieldList {
			// 结构体转 Promql contains 和 ncontains 转为特殊的正则 contains ["a","b"] => =~"a|b"
			q.Conditions.FieldList[i] = *(cond.ContainsToPromReg())
		}
	}

	// 是否要过滤condition，获取data_id_list
	if opt.IsFilterCond {
		// 如果传了tableID，则可以找到唯一的库表，不需要再用dataID遍历
		tableIDFilter, err1 := NewTableIDFilter(string(q.FieldName), q.TableID, q.DataIDList, q.Conditions)
		if err1 != nil && err1 != ErrEmptyTableID {
			return clusterID, db, measurement, metricName, err1
		}

		q.DataIDList = tableIDFilter.DataIDList()
	}

	return clusterID, db, measurement, metricName, err
}

// ToProm 转换为Prom对应的节点信息, 整个转换逻辑是从内到外的构造：vectorSelector、Call
// 注意：此处需要提供一个ctx，原因是
// 1. 如果提供的过滤信息中存在or拼接的时候，需要过滤条件打入到context中；因为promql的label过滤并不支持or
// 2. 如果提供的过滤信息中存在limit信息，需要将过滤的条件打入到context中，因为promql并没有类似的过滤条件
func (q *QueryParams) ToProm(ctx context.Context, options *Option) (*PromExpr, error) {
	var (
		labelList []*labels.Matcher
		err       error

		result = new(PromExpr)

		totalBuffer [][]ConditionField
		offset      time.Duration

		queryInfo *promql.QueryInfo

		step time.Duration

		dTmp model.Duration

		originalOffset time.Duration
	)

	clusterID, db, measurement, realMetricName, err := q.checkOption(ctx, options)
	if err != nil {
		return nil, err
	}

	// 1. 构造vectorSelector
	// 判断获取对应的labelMatcher
	if labelList, totalBuffer, err = q.Conditions.ToProm(); err != nil {
		log.Errorf(context.TODO(), "failed to translate conditions to prom ast for->[%s]", err)
		return nil, err
	}

	// 加入指标本身到matcher信息当中
	labelList = append(labelList, labels.MustNewMatcher(labels.MatchEqual, labels.MetricName, realMetricName))
	log.Debugf(context.TODO(), "metric->[%s] with label->[__name__]", realMetricName)

	// 如果开启了与influxdb结果对齐的模式，则数据将进行特殊的偏移和聚合处理
	// influxdb查询为时间戳向后查询，且区间为左闭右开
	// promql查询为时间戳向前查询，且区间为左闭右闭
	// 这里通过调整offset，实现将promql的查询结果基本等效于influxdb的效果
	// 当查最新数据时，上面通过offset调整会造成最后一个 window 区间范围内的结果错误，所以此处改为使用step对齐, 这样仍然会造成最后一个点错误
	// 但为保持和influxdb对齐，目前先采用此方案
	if q.AlignInfluxdbResult && q.TimeAggregation.Window != "" {
		step = promql.GetDefaultStep()
		if q.Step != "" {
			dTmp, err = model.ParseDuration(q.Step)
			if err != nil {
				log.Errorf(context.TODO(), "parse step err->[%s]", err)
				return nil, err
			}
			step = time.Duration(dTmp)
		}
		// 控制偏移，promQL 只支持毫秒级别数据
		originalOffset = -step + time.Millisecond
	}

	if q.Offset != "" {
		dTmp, err = model.ParseDuration(q.Offset)
		if err != nil {
			return nil, err
		}
		offset = time.Duration(dTmp)
		if q.OffsetForward {
			// 时间戳向后平移，查询后面的数据
			originalOffset -= offset
		} else {
			// 时间戳向前平移，查询前面的数据
			originalOffset += offset
		}
	}

	queryInfo = new(promql.QueryInfo)

	if len(q.AggregateMethodList) > 0 && !options.IsOnlyParse {
		if q.TimeAggregation.Function == CountOverTime && q.AggregateMethodList[0].Method == "sum" {
			q.TimeAggregation.Function = SumOverTime
			queryInfo.IsCount = true
		}
	}

	result.Expr = &parser.VectorSelector{
		Name:           realMetricName,
		LabelMatchers:  labelList,
		Offset:         q.VectorOffset,
		Timestamp:      q.Timestamp,
		StartOrEnd:     q.StartOrEnd,
		OriginalOffset: originalOffset,
	}

	// 传入了window，则进行window计算
	if q.TimeAggregation.Function != "" && q.TimeAggregation.Window != "" {
		result.Expr, err = q.TimeAggregation.ToProm(result.Expr)
		if err != nil {
			log.Errorf(context.TODO(), "failed to parse window function for->[%s]", err)
			return nil, err
		}
		log.Debugf(context.TODO(), "function->[%s] with window->[%s]will add to expr",
			q.TimeAggregation.Function, q.TimeAggregation.Window)
	}

	queryInfo.AggregateMethodList = make([]promql.AggrMethod, 0, len(q.AggregateMethodList))
	// 循环封装method
	for _, method := range q.AggregateMethodList {
		if result.Expr, err = method.ToProm(result.Expr); err != nil {
			log.Errorf(context.TODO(), "failed to translate function for->[%s]", err)
			return nil, err
		}
		queryInfo.AggregateMethodList = append(queryInfo.AggregateMethodList, promql.AggrMethod{
			Name:       method.Method,
			Dimensions: method.Dimensions,
			Without:    method.Without,
		})
		log.Debugf(context.TODO(),
			"function->[%s] args->[%s] dimension->[%s] is add to list.", method.Method, method.VArgsList, method.Dimensions,
		)
	}
	result.Dimensions = q.Dimensions
	queryInfo.Conditions = ConvertToPromBuffer(totalBuffer)
	queryInfo.OffsetInfo = promql.OffSetInfo{
		OffSet:  offset,
		Limit:   int(q.Limit),
		SLimit:  q.Slimit,
		SOffSet: q.Soffset,
	}

	// 增加 spaceUid 过滤
	if options.SpaceUid != "" {
		tsDBs, err1 := GetTsDBList(ctx, &TsDBOption{
			SpaceUid:  options.SpaceUid,
			TableID:   q.TableID,
			FieldName: string(q.FieldName),
		})
		if err1 != nil {
			return nil, err1
		}
		queryInfo.TsDBs = tsDBs

		// 纯转换不需要再写入到 ctx，避免重复写入，因为转换完之后会修改结构体，如果再次解析则数据不对
		if !options.IsOnlyParse {
			result.ctx, err1 = promql.QueryInfoIntoContext(ctx, realMetricName, string(q.FieldName), queryInfo)
			if err1 != nil {
				return nil, err1
			}
		}
		return result, nil
	}

	// 将Offset等信息，加入到ctx中
	// 这里由于上面将route解析错误时，不直接返回，而是试图拿dataIDList, 所以这里加一个防御机制
	queryInfo.ClusterID = clusterID
	queryInfo.DB = db
	queryInfo.Measurement = measurement
	queryInfo.DataIDList = q.DataIDList
	queryInfo.IsPivotTable = q.IsFreeSchema

	// 纯转换不需要再写入到 ctx，避免重复写入，因为转换完之后会修改结构体，如果再次解析则数据不对
	if !options.IsOnlyParse {
		result.ctx, err = promql.QueryInfoIntoContext(ctx, realMetricName, string(q.FieldName), queryInfo)
		if err != nil {
			return nil, err
		}
	}
	log.Debugf(context.TODO(), "get query info:%v", queryInfo)

	return result, nil
}

// AnalysisQuery
func AnalysisQuery(stmt string) (*CombinedQueryParams, error) {
	var query *CombinedQueryParams
	err := json.Unmarshal([]byte(stmt), &query)
	if err != nil {
		return nil, err
	}
	// 传递时间给指标查询列表
	for _, q := range query.QueryList {
		q.Start = query.Start
		q.End = query.End
		q.Step = query.Step
	}
	return query, nil
}

// QueryProm  将结构化查询 -> promql -> 判断是否去查询
func QueryProm(ctx context.Context, query *CombinedQueryParams, options *Option) (context.Context, string, error) {
	// 遍历判断每张表是否要按freeSchema处理
	for _, q := range query.QueryList {
		if q.IsFreeSchema {
			continue
		}
		q.IsFreeSchema = influxdb.IsPivotTable(string(q.TableID))
	}
	result, err := query.ToProm(ctx, options)
	if err != nil {
		return nil, "", err
	}
	return result.GetCtx(), result.GetExpr().String(), nil
}
