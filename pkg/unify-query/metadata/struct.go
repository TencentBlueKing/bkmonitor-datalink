// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// AggrMethod 聚合方法
type AggrMethod struct {
	Name       string
	Dimensions []string
	Without    bool
}

// OffSetInfo Offset的信息存储，供promql查询转换为influxdb查询语句时使用
type OffSetInfo struct {
	OffSet  time.Duration
	Limit   int
	SOffSet int
	SLimit  int
}

// Query 查询扩展信息，为后面查询提供定位
type Query struct {
	SourceType string // 查询数据源 InfluxDB 或者 VictoriaMetrics
	Password   string // 查询鉴权

	ClusterID string // 存储 ID

	StorageType string // 存储类型
	StorageID   string
	ClusterName string
	TagsKey     []string

	// vm 的 rt
	TableID        string
	VmRt           string
	IsSingleMetric bool

	// 兼容 InfluxDB 结构体
	RetentionPolicy string // 存储 RP
	DB              string // 存储 DB
	Measurement     string // 存储 Measurement
	Field           string // 存储 Field

	IsHasOr bool // 标记是否有 or 条件

	AggregateMethodList []AggrMethod // 聚合方法列表，从内到外排序

	Condition string // 过滤条件

	Filters []map[string]string // 查询中自带查询条件，用于拼接

	OffsetInfo OffSetInfo // limit等偏移量配置

	SegmentedEnable bool // 是否开启分段查询
}

type QueryList []*Query

type QueryMetric struct {
	QueryList QueryList

	ReferenceName string
	MetricName    string
	IsCount       bool // 标记是否为 count 方法
}

type QueryReference map[string]*QueryMetric

type Queries struct {
	Query QueryReference

	ctx                   context.Context
	directlyClusterID     string
	directlyMetricName    map[string]string
	directlyLabelsMatcher map[string][]*labels.Matcher
	directlyResultTable   map[string][]string
}

func (qRef QueryReference) GetVMFeatureFlag(ctx context.Context) bool {
	var (
		span oleltrace.Span
		user = GetUser(ctx)
	)
	ctx, span = trace.IntoContext(ctx, trace.TracerName, "check-vm-feature-flag")
	if span != nil {
		defer span.End()
	}

	// 特性开关只有指定空间才启用 vm 查询
	ffUser := featureFlag.FFUser(span.SpanContext().TraceID().String(), map[string]interface{}{
		"name":     user.Name,
		"source":   user.Source,
		"spaceUid": user.SpaceUid,
	})

	vmQuery := featureFlag.BoolVariation(ctx, ffUser, "vm-query", false)
	trace.InsertStringIntoSpan("vm-query-feature-flag", fmt.Sprintf("%v:%v", ffUser.GetCustom(), vmQuery), span)

	return vmQuery
}
func (qRef QueryReference) CheckVmQuery(ctx context.Context, vmQuery bool) (bool, map[string]string, map[string][]string, error) {
	var (
		span      oleltrace.Span
		metricMap = make(map[string]string)
		vmRtGroup = make(map[string][]string)
		err       error
		ok        bool

		orCondition string
	)
	ctx, span = trace.IntoContext(ctx, trace.TracerName, "check-vm-query")
	if span != nil {
		defer span.End()
	}

	if !vmQuery {
		return ok, metricMap, vmRtGroup, err
	}

	for referenceName, reference := range qRef {
		if len(reference.QueryList) > 0 {
			var (
				metricName string
				vmRts      = make(map[string]struct{})
			)

			trace.InsertIntIntoSpan(fmt.Sprintf("result_table_%s_num", referenceName), len(reference.QueryList), span)

			for _, query := range reference.QueryList {
				var traceLog bytes.Buffer

				if query.IsHasOr {
					orCondition = query.Condition
					traceLog.WriteString(fmt.Sprintf("or_condition: %s, ", orCondition))
				}
				// 获取 vm 的指标名
				metricName = fmt.Sprintf("%s_%s", query.Measurement, query.Field)

				traceLog.WriteString(fmt.Sprintf("metric_name: %s, ", metricName))
				traceLog.WriteString(fmt.Sprintf("is-split: %v, ", query.IsSingleMetric))
				traceLog.WriteString(fmt.Sprintf("vm-rt: %v, ", query.VmRt))

				trace.InsertStringIntoSpan(fmt.Sprintf("result_table_%s_%s", referenceName, query.DB), traceLog.String(), span)

				// 只有全部为单指标单表
				if !query.IsSingleMetric {
					return ok, metricMap, vmRtGroup, err
				}

				// 获取 vm 对应的 rt 列表
				if query.VmRt != "" {
					// 如果有拆分表默认维度列表中的维度，则按照固定协议拼接vmrt的表名
					var dimensionFlag uint
					// 获取聚合方法列表
					for _, amList := range query.AggregateMethodList {
						// 获取维度列表
						for _, amDimension := range amList.Dimensions {
							// 维度判断（两个维度同时出现才拼接）
							switch amDimension {
							case "bk_obj_id":
								dimensionFlag |= 1
							case "bk_inst_id":
								dimensionFlag |= 2
							}
							if dimensionFlag == 3 {
								query.VmRt = strings.Replace(query.VmRt, "_raw", "_cmdb", 1)
								break
							}
						}
						if dimensionFlag == 3 {
							break
						}
					}
					vmRts[query.VmRt] = struct{}{}
				}
			}
			metricMap[referenceName] = metricName
			if len(vmRts) == 0 {
				err = fmt.Errorf("vm query result table is empty %s", metricName)
				break
			}

			vmRtGroup[metricName] = make([]string, 0, len(vmRts))
			for k := range vmRts {
				vmRtGroup[metricName] = append(vmRtGroup[metricName], k)
			}
		}
	}

	ok = true
	if orCondition != "" {
		err = fmt.Errorf("vm query is not support conditions with or: %s", orCondition)
	}

	return ok, metricMap, vmRtGroup, err
}
