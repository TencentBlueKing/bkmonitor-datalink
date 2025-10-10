// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-gota/gota/dataframe"
	"github.com/go-gota/gota/series"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

var _ tsdb.Instance = (*Instance)(nil)

// Instance redis 查询实例
type Instance struct {
	tsdb.DefaultInstance

	Ctx                 context.Context
	Timeout             time.Duration
	ClusterMetricPrefix string
}

func (i *Instance) Check(ctx context.Context, promql string, start, end time.Time, step time.Duration) string {
	return ""
}

func (i *Instance) QuerySeriesSet(ctx context.Context, query *metadata.Query, start, end time.Time) storage.SeriesSet {
	return nil
}

func (i *Instance) QueryExemplar(ctx context.Context, fields []string, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) (*decoder.Response, error) {
	return nil, nil
}

func (i *Instance) QueryLabelNames(ctx context.Context, query *metadata.Query, start, end time.Time) ([]string, error) {
	return nil, nil
}

func (i *Instance) QueryLabelValues(ctx context.Context, query *metadata.Query, name string, start, end time.Time) ([]string, error) {
	return nil, nil
}

func (i *Instance) QuerySeries(ctx context.Context, query *metadata.Query, start, end time.Time) ([]map[string]string, error) {
	return nil, nil
}

func (i *Instance) DirectLabelNames(ctx context.Context, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	return nil, nil
}

func (i *Instance) DirectLabelValues(ctx context.Context, name string, start, end time.Time, limit int, matchers ...*labels.Matcher) ([]string, error) {
	return nil, nil
}

func (i *Instance) InstanceType() string {
	return metadata.RedisStorageType
}

func (i *Instance) DirectQuery(ctx context.Context, qs string, end time.Time) (promql.Vector, error) {
	df, err := i.rawQuery(ctx, time.Time{}, end, time.Duration(0))
	if err != nil {
		return nil, err
	}
	return i.vectorFormat(ctx, *df)
}

func (i *Instance) DirectQueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) (promql.Matrix, bool, error) {
	df, err := i.rawQuery(ctx, start, end, step)
	if err != nil {
		return nil, false, err
	}
	return i.matrixFormat(ctx, *df)
}

func (i *Instance) rawQuery(ctx context.Context, start, end time.Time, step time.Duration) (*dataframe.DataFrame, error) {
	var startAnaylize time.Time

	// 根据现有支持情况检查 QueryTs 请求体
	query := metadata.GetQueryClusterMetric(ctx)

	metricName := query.MetricName
	// 要求必须传入集群过滤条件，并且汇总所有相关集群数据，预加载数据
	clusterNames := make([]string, 0)
	for _, conds := range query.Conditions {
		for _, cond := range conds {
			if cond.Operator == structured.ConditionEqual && cond.DimensionName == ClusterMetricFieldClusterName {
				clusterNames = append(clusterNames, cond.Value...)
			}
		}
	}
	if len(clusterNames) == 0 {
		return nil, errors.Errorf("Dimension(%s) must be passed in query-condition ", ClusterMetricFieldClusterName)
	}
	stoCtx, _ := context.WithTimeout(ctx, i.Timeout)
	startAnaylize = time.Now()

	sto := MetricStorage{ctx: stoCtx, storagePrefix: i.ClusterMetricPrefix}
	metricMeta, err := sto.GetMetricMeta(metricName)
	if err != nil {
		_ = metadata.Sprintf(
			metadata.MsgQueryRedis,
			"查询异常",
		).Error(ctx, err)
		return &dataframe.DataFrame{}, nil
	}
	df, opts := metricMeta.toDataframe()
	for _, clusterName := range clusterNames {
		dfPointer, err := sto.LoadMetricDataFrame(metricName, clusterName, opts)
		if err != nil {
			metadata.Sprintf(
				metadata.MsgQueryRedis,
				"查询异常 %+v",
				err,
			).Warn(ctx)
			continue
		}
		if dfPointer.Nrow() > 0 {
			df = df.RBind(*dfPointer)
		}
	}
	df = i.handleDFQuery(df, query, start, end, step)
	if df.Error() != nil {
		return nil, df.Error()
	}
	queryCost := time.Since(startAnaylize)
	metric.TsDBRequestSecond(
		ctx, queryCost, i.InstanceType(), "",
	)

	return &df, nil
}

func (i *Instance) vectorFormat(ctx context.Context, df dataframe.DataFrame) (promql.Vector, error) {
	vector := make(promql.Vector, 0)
	matrix, _, err := i.matrixFormat(ctx, df)
	if err != nil {
		return nil, err
	}
	for _, mSeries := range matrix {
		vector = append(vector, promql.Sample{
			Metric: mSeries.Metric,
			Point:  mSeries.Points[len(mSeries.Points)-1],
		})
	}
	return vector, nil
}

func (i *Instance) matrixFormat(ctx context.Context, df dataframe.DataFrame) (promql.Matrix, bool, error) {
	names := df.Names()
	groupPoints := map[string]promql.Series{}
	for idx, row := range df.Records() {
		// 跳过第一行（表头行）
		if idx == 0 {
			continue
		}
		// 处理一行完整的数据，分桶塞点
		labelsGroup, point, err := arrToPoint(names, row)
		if err != nil {
			return nil, false, err
		}
		h := consul.HashIt(labelsGroup)
		var oneSeries promql.Series
		var ok bool
		if oneSeries, ok = groupPoints[h]; ok {
			oneSeries.Points = append(oneSeries.Points, *point)
		} else {
			oneSeries = promql.Series{
				Metric: labelsGroup,
				Points: []promql.Point{*point},
			}
		}
		groupPoints[h] = oneSeries
	}
	matrix := make(promql.Matrix, 0)
	for _, mSeries := range groupPoints {
		matrix = append(matrix, mSeries)
	}
	return matrix, false, nil
}

func arrToPoint(colNames []string, row []string) (labels.Labels, *promql.Point, error) {
	var err error
	labelsGroup := make(labels.Labels, 0)
	point := promql.Point{}

	for idx, val := range row {
		if colNames[idx] == ClusterMetricFieldValName {
			point.V, err = strconv.ParseFloat(val, 64)
			if err != nil {
				return nil, nil, errors.Wrap(err, "Invalid cluster metric value type")
			}
		} else if colNames[idx] == ClusterMetricFieldTimeName {
			point.T, err = strconv.ParseInt(val, 10, 64)
			if err != nil {
				return nil, nil, errors.Wrap(err, "Invalid cluster metric time type")
			}
			// 秒转毫秒
			point.T = point.T * 1000
		} else {
			labelsGroup = append(labelsGroup, labels.Label{Name: colNames[idx], Value: val})
		}
	}
	return labelsGroup, &point, nil
}

// handleDFQuery 根据传入的查询配置，处理 DF 数据
func (i *Instance) handleDFQuery(
	df dataframe.DataFrame, query *metadata.QueryClusterMetric, start, end time.Time, step time.Duration,
) dataframe.DataFrame {
	// 时间过滤
	df = df.FilterAggregation(
		dataframe.And,
		dataframe.F{Colname: ClusterMetricFieldTimeName, Comparator: series.GreaterEq, Comparando: int(start.Unix())},
		dataframe.F{Colname: ClusterMetricFieldTimeName, Comparator: series.LessEq, Comparando: int(end.Unix())},
	)
	// 字段过滤
	mergedDF := dataframe.DataFrame{}
	orFields := query.Conditions
	// 每个分组过滤条件处理后，把结果进行合并
	for _, fields := range orFields {
		dfConditions := make([]dataframe.F, 0)
		for _, fieldCond := range fields {
			dfComparator, ok := QueryConditionToDataframeComparator[fieldCond.Operator]
			if !ok {
				return dataframe.DataFrame{Err: errors.Errorf("Not suppport condition operator: %v ", fieldCond.Operator)}
			}
			dfConditions = append(
				dfConditions,
				dataframe.F{Colname: fieldCond.DimensionName, Comparator: dfComparator, Comparando: fieldCond.Value})
		}
		filterDF := df.FilterAggregation(dataframe.And, dfConditions...)
		if filterDF.Nrow() != 0 {
			if mergedDF.Nrow() == 0 {
				mergedDF = filterDF
			} else {
				mergedDF = mergedDF.RBind(filterDF)
			}
		}
	}
	df = mergedDF

	if df.Nrow() == 0 {
		return df
	}

	// 根据时间聚合函数处理数据
	if query.TimeAggregation.Function != "" {
		allDims := make([]string, 0, df.Ncol()-2)
		for _, col := range df.Names() {
			if col != ClusterMetricFieldValName {
				allDims = append(allDims, col)
			}
		}
		df = handleDFTimeRounding(df, query.TimeAggregation.WindowDuration)
		df = handleDFGroupBy(df, allDims, query.TimeAggregation.Function)
		if df.Error() != nil {
			return df
		}
	}

	// 分组聚合，仅支持一个聚合
	if len(query.Aggregates) == 1 {
		aggre := query.Aggregates[0]
		aggre.Dimensions = append(aggre.Dimensions, ClusterMetricFieldTimeName)
		df = handleDFTimeRounding(df, step)
		df = handleDFGroupBy(df, aggre.Dimensions, aggre.Name)
		if df.Error() != nil {
			return df
		}
	} else if len(query.Aggregates) > 1 {
		return dataframe.DataFrame{Err: errors.Errorf("Only one aggregate method can be supported.")}
	}
	// 按照时间字段进行排序
	df = df.Arrange(dataframe.Sort(ClusterMetricFieldTimeName))
	return df
}

// handleDFGroupBy 处理 dataframe 按照时间字段取整
func handleDFTimeRounding(df dataframe.DataFrame, step time.Duration) dataframe.DataFrame {
	if step == 0 {
		return df
	}
	stepSecond := int(step / time.Second)
	timeSeries := df.Col(ClusterMetricFieldTimeName)
	timeSeries.Map(func(element series.Element) series.Element {
		val := element.Val().(int)
		newVal := int(val/stepSecond) * stepSecond
		element.Set(newVal)
		return element
	})
	df = df.Mutate(timeSeries)
	return df
}

// handleDFGroupBy 处理 dataframe 聚合操作
func handleDFGroupBy(df dataframe.DataFrame, dims []string, aggreFunc string) dataframe.DataFrame {
	groups := df.GroupBy(dims...)
	aggreType, ok := QueryAggreToDataframeMapping[aggreFunc]
	if !ok {
		return dataframe.DataFrame{Err: errors.Errorf("Not support aggregate method: %s ", aggreFunc)}
	}
	df = groups.Aggregation([]dataframe.AggregationType{aggreType}, []string{"value"})
	// 将分组聚合字段名称调整为 value
	df = df.Rename("value", fmt.Sprintf("value_%s", aggreType.String()))
	return df
}

type MetricStorage struct {
	ctx           context.Context
	storagePrefix string
}

type MetricMeta struct {
	MetricName string   `json:"metric_name"`
	Tags       []string `json:"tags"`
}

func (m *MetricMeta) toDataframe() (dataframe.DataFrame, []dataframe.LoadOption) {
	opts := []dataframe.LoadOption{
		dataframe.DetectTypes(false),
		dataframe.DefaultType(series.String),
		dataframe.WithTypes(map[string]series.Type{
			ClusterMetricFieldValName:  series.Float,
			ClusterMetricFieldTimeName: series.Int,
		}),
	}
	columns := []string{ClusterMetricFieldValName, ClusterMetricFieldTimeName}
	values := []string{"0", "0"}
	for _, tag := range m.Tags {
		columns = append(columns, tag)
		values = append(values, "")
	}
	df := dataframe.LoadRecords([][]string{columns, values}, opts...)
	return df, opts
}

func (sto *MetricStorage) GetMetricMeta(metricName string) (*MetricMeta, error) {
	var (
		metricMeta = &MetricMeta{}
		res        string
		err        error
	)
	metaKey := fmt.Sprintf("%s:%s", sto.storagePrefix, ClusterMetricMetaKey)
	res, err = redisUtil.HGet(sto.ctx, metaKey, metricName)
	if err != nil {
		return nil, errors.Wrap(err, "Fail to get cluster metric meta from redis")
	}
	err = json.Unmarshal([]byte(res), metricMeta)
	if err != nil {
		return nil, errors.Wrap(err, "Fail to unmarshal cluster metric meta")
	}
	return metricMeta, nil
}

func (sto *MetricStorage) LoadMetricDataFrame(metricName string, clusterName string, opts []dataframe.LoadOption) (*dataframe.DataFrame, error) {
	var (
		field string
		err   error
		res   string
	)
	if err != nil {
		return nil, err
	}
	field = strings.Replace(
		ClusterMetricFieldPattern, fmt.Sprintf("{%s}", ClusterMetricFieldClusterName), clusterName, -1)
	field = strings.Replace(field, fmt.Sprintf("{%s}", ClusterMetricFieldMetricName), metricName, -1)

	dataKey := fmt.Sprintf("%s:%s", sto.storagePrefix, ClusterMetricKey)
	res, err = redisUtil.HGet(sto.ctx, dataKey, field)
	if err != nil {
		return nil, err
	}
	df := dataframe.ReadJSON(strings.NewReader(res), opts...)
	if df.Error() != nil {
		return nil, df.Error()
	}
	return &df, nil
}
