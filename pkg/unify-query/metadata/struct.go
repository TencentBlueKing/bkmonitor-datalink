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
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/VictoriaMetrics/metricsql"
	"github.com/prometheus/prometheus/model/labels"
	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const (
	StaticField = "value"
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
	RetentionPolicy string   // 存储 RP
	DB              string   // 存储 DB
	Measurement     string   // 存储 Measurement
	Field           string   // 存储 Field
	Timezone        string   // 存储 Timezone
	Fields          []string // 存储命中的 Field 列表，一般情况下为一个，当 Field 为模糊匹配时，解析为多个
	Measurements    []string // 存储命中的 Measurement 列表，一般情况下为一个，当 Measurement 为模糊匹配时，解析为多个

	// 用于 promql 查询
	LabelsMatcher []*labels.Matcher
	IsHasOr       bool // 标记是否有 or 条件

	AggregateMethodList []AggrMethod // 聚合方法列表，从内到外排序

	Condition string // 过滤条件

	VmCondition    string
	VmConditionNum int

	Filters []map[string]string // 查询中自带查询条件，用于拼接

	OffsetInfo OffSetInfo // limit等偏移量配置

	SegmentedEnable bool // 是否开启分段查询
}

type QueryList []*Query

type QueryMetric struct {
	QueryList QueryList

	ReferenceName string
	MetricName    string

	IsCount bool // 标记是否为 count 方法
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

// CheckDruidCheck 判断是否是查询 druid 数据
func (qRef QueryReference) CheckDruidCheck(ctx context.Context) bool {
	// 判断是否打开 druid-query 特性开关
	if !GetDruidQueryFeatureFlag(ctx) {
		return false
	}

	druidCheckStatus := false
	for _, reference := range qRef {
		if len(reference.QueryList) > 0 {
			for _, query := range reference.QueryList {
				// 如果 vmRT 为空，则不进行判断
				if query.VmRt == "" {
					continue
				}

				druidDimsStatus := map[string]struct{}{
					"bk_obj_id":  {},
					"bk_inst_id": {},
				}

				tags, _ := ParseCondition(query.Condition)

				for _, tag := range tags {
					if _, ok := druidDimsStatus[string(tag.Key)]; ok {
						druidCheckStatus = true
					}
				}

				if !druidCheckStatus {
					for _, amList := range query.AggregateMethodList {
						for _, amDimension := range amList.Dimensions {
							if _, ok := druidDimsStatus[amDimension]; ok {
								druidCheckStatus = true
								break
							}
						}
					}
				}

				if druidCheckStatus {
					// 替换 vmrt 的值
					oldVmRT := query.VmRt
					newVmRT := strings.Replace(oldVmRT, "_raw", "_cmdb", 1)

					if newVmRT != oldVmRT {
						query.VmRt = newVmRT
					}

					expr, err := metricsql.Parse(fmt.Sprintf(`{%s}`, query.VmCondition))
					if err != nil {
						log.Errorf(ctx, fmt.Sprintf("metricsql parse error: %s", err.Error()))
						return false
					}

					me, ok := expr.(*metricsql.MetricExpr)
					if ok {
						var condition []byte
						for i, f := range me.LabelFilterss {
							var dst []byte
							for j, l := range f {
								if l.Label == "result_table_id" {
									l.Value = strings.Replace(l.Value, oldVmRT, newVmRT, 1)
								}

								if !query.IsSingleMetric {
									oldMetric := fmt.Sprintf("%s_%s", query.Measurement, query.Field)
									newMetric := fmt.Sprintf("%s_%s", query.Field, StaticField)

									if l.Label == "__name__" {
										l.Value = strings.Replace(l.Value, oldMetric, newMetric, 1)
									}
								}

								if j == 0 {
									dst = l.AppendString(dst)
								} else {
									dst = append(dst, ',')
									dst = l.AppendString(dst)
								}
							}

							if i == 0 {
								condition = dst
							} else {
								condition = append(condition, " or "...)
								condition = append(condition, dst...)
							}
						}
						query.VmCondition = string(condition)
					}

					query.IsSingleMetric = true
				}
			}
		}
	}

	return druidCheckStatus
}

// CheckVmQuery 判断是否是查询 vm 数据
func (qRef QueryReference) CheckVmQuery(ctx context.Context) (bool, *VmExpand, error) {
	var (
		span oleltrace.Span
		err  error
		ok   bool

		vmExpand = &VmExpand{
			MetricAliasMapping:    make(map[string]string),
			MetricFilterCondition: make(map[string]string),
			ResultTableGroup:      make(map[string][]string),
			LabelsMatcher:         make(map[string][]*labels.Matcher),
			ConditionNum:          0,
		}
	)
	ctx, span = trace.IntoContext(ctx, trace.TracerName, "check-vm-query")
	if span != nil {
		defer span.End()
	}

	// 特性开关 vm or 语法查询
	vmQueryFeatureFlag := GetVMQueryFeatureFlag(ctx)
	druidQueryStatus := qRef.CheckDruidCheck(ctx)

	// 未开启 vm-query 特性开关 并且 不是 druid-query ，则不使用 vm 查询能力
	if !vmQueryFeatureFlag && !druidQueryStatus {
		return ok, vmExpand, err
	}

	isOrQuery := false

	for referenceName, reference := range qRef {
		if 0 < len(reference.QueryList) {
			var (
				metricName string
				vmRts      = make(map[string]struct{})
			)

			trace.InsertIntIntoSpan(fmt.Sprintf("result_table_%s_num", referenceName), len(reference.QueryList), span)

			vmConditions := make(map[string]struct{})

			for _, query := range reference.QueryList {
				// 获取 vm 的指标名
				metricName = fmt.Sprintf("%s_%s", query.Measurement, query.Field)

				// 只有全部为单指标单表
				if !query.IsSingleMetric {
					return ok, vmExpand, err
				}

				// 开启 vm rt 才进行 vm 查询
				if query.VmRt != "" {
					if query.IsHasOr {
						isOrQuery = query.IsHasOr
					}

					if query.VmCondition != "" {
						vmConditions[query.VmCondition] = struct{}{}
					}

					vmExpand.ConditionNum += query.VmConditionNum

					// labels matcher 不支持 or 语法，所以只取一个
					if len(query.LabelsMatcher) > 0 {
						vmExpand.LabelsMatcher[referenceName] = query.LabelsMatcher
					}

					// 获取 vm 对应的 rt 列表
					vmRts[query.VmRt] = struct{}{}
				}
			}

			metricFilterCondition := ""
			if len(vmConditions) > 0 {
				vmc := make([]string, 0, len(vmConditions))
				for k := range vmConditions {
					vmc = append(vmc, k)
				}

				metricFilterCondition = fmt.Sprintf(`%s`, strings.Join(vmc, ` or `))
				if len(vmConditions) > 1 {
					isOrQuery = true
				}
			}

			vmExpand.MetricFilterCondition[referenceName] = metricFilterCondition
			vmExpand.MetricAliasMapping[referenceName] = metricName

			if len(vmRts) == 0 {
				err = fmt.Errorf("vm query result table is empty %s", metricName)
				break
			}

			if vmExpand.ResultTableGroup[referenceName] == nil {
				vmExpand.ResultTableGroup[referenceName] = make([]string, 0)
			}
			for k := range vmRts {
				vmExpand.ResultTableGroup[referenceName] = append(vmExpand.ResultTableGroup[referenceName], k)
			}

			sort.Strings(vmExpand.ResultTableGroup[referenceName])
		}
	}

	trace.InsertStringIntoSpan("vm-query-or", fmt.Sprintf("%v", isOrQuery), span)

	ok = true
	return ok, vmExpand, err
}
