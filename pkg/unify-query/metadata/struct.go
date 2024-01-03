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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const (
	StaticField = "value"

	UUID = "query_uuid"
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
	StorageName string

	ClusterName string
	TagsKey     []string

	TableID string

	// vm 的 rt
	VmRt string

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

	// BkSql 过滤条件
	BkSqlCondition string

	// Vm 过滤条件
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

type ReplaceLabels map[string]ReplaceLabel

type ReplaceLabel struct {
	Source string
	Target string
}

func ReplaceVmCondition(condition string, replaceLabels ReplaceLabels) string {
	if len(replaceLabels) == 0 {
		return condition
	}

	expr, err := metricsql.Parse(fmt.Sprintf(`{%s}`, condition))
	if err != nil {
		return condition
	}

	me, ok := expr.(*metricsql.MetricExpr)
	if !ok {
		return condition
	}

	var cond []byte
	for i, f := range me.LabelFilterss {
		var dst []byte
		for j, l := range f {
			if rl, exist := replaceLabels[l.Label]; exist {
				if l.Value == rl.Source {
					l.Value = rl.Target
				}
			}

			if j == 0 {
				dst = l.AppendString(dst)
			} else {
				dst = append(dst, ',', ' ')
				dst = l.AppendString(dst)
			}
		}

		if i == 0 {
			cond = dst
		} else {
			cond = append(cond, " or "...)
			cond = append(cond, dst...)
		}
	}

	return string(cond)
}

func (qRef QueryReference) CheckMustVmQuery(ctx context.Context) bool {
	for _, reference := range qRef {
		if len(reference.QueryList) > 0 {
			for _, query := range reference.QueryList {
				// 忽略 vmRt 为空的
				if query.VmRt == "" {
					return false
				}

				// 如果该 TableID 未配置特性开关则认为不能访问 vm，直接返回 false
				if !GetMustVmQueryFeatureFlag(ctx, query.TableID) {
					return false
				}
			}
		}
	}

	for _, reference := range qRef {
		for _, query := range reference.QueryList {
			query.IsSingleMetric = true
		}
	}

	return true
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
					replaceLabels := make(ReplaceLabels)

					// 替换 vmrt 的值
					oldVmRT := query.VmRt
					newVmRT := strings.Replace(oldVmRT, "_raw", "_cmdb", 1)

					if newVmRT != oldVmRT {
						query.VmRt = newVmRT

						replaceLabels["result_table_id"] = ReplaceLabel{
							Source: oldVmRT,
							Target: newVmRT,
						}
					}

					if !query.IsSingleMetric {
						query.IsSingleMetric = true
					}

					query.VmCondition = ReplaceVmCondition(query.VmCondition, replaceLabels)
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
			MetricFilterCondition: make(map[string]string),
		}
	)
	ctx, span = trace.IntoContext(ctx, trace.TracerName, "check-vm-query")
	if span != nil {
		defer span.End()
	}

	// 特性开关 vm or 语法查询
	vmQueryFeatureFlag := GetVMQueryFeatureFlag(ctx)
	druidQueryStatus := qRef.CheckDruidCheck(ctx)
	mustVmQueryStatus := qRef.CheckMustVmQuery(ctx)

	// 未开启 vm-query 特性开关 并且 不是 druid-query ，则不使用 vm 查询能力
	if !vmQueryFeatureFlag && !druidQueryStatus && !mustVmQueryStatus {
		return ok, nil, err
	}

	var (
		vmRts          = make(map[string]struct{})
		vmClusterNames = make(map[string]struct{})
	)

	for referenceName, reference := range qRef {
		if 0 < len(reference.QueryList) {
			trace.InsertIntIntoSpan(fmt.Sprintf("result_table_%s_num", referenceName), len(reference.QueryList), span)

			vmConditions := make(map[string]struct{})

			for _, query := range reference.QueryList {

				// 该字段表示为是否查 VM
				if !query.IsSingleMetric {
					return ok, nil, err
				}

				// 开启 vm rt 才进行 vm 查询
				if query.VmRt != "" {
					if query.VmCondition != "" {
						vmConditions[query.VmCondition] = struct{}{}
					}

					vmExpand.ConditionNum += query.VmConditionNum

					// 获取 vm 对应的 rt 列表
					vmRts[query.VmRt] = struct{}{}

					// 获取 vm 对应的 clusterName，因为存在混用的情况，所以也需要把空也放到里面
					vmClusterNames[query.StorageName] = struct{}{}
				}
			}

			metricFilterCondition := ""
			if len(vmConditions) > 0 {
				vmc := make([]string, 0, len(vmConditions))
				for k := range vmConditions {
					vmc = append(vmc, k)
				}

				metricFilterCondition = fmt.Sprintf(`%s`, strings.Join(vmc, ` or `))
			}

			vmExpand.MetricFilterCondition[referenceName] = metricFilterCondition
		}
	}

	trace.InsertStringIntoSpan("vm_expand_cluster_name", fmt.Sprintf("%+v", vmClusterNames), span)

	// 当所有的 vm 集群都一样的时候，才进行传递
	if len(vmClusterNames) == 1 {
		for k := range vmClusterNames {
			vmExpand.ClusterName = k
		}
	}

	vmExpand.ResultTableList = make([]string, 0, len(vmRts))
	for k := range vmRts {
		vmExpand.ResultTableList = append(vmExpand.ResultTableList, k)
	}

	sort.Strings(vmExpand.ResultTableList)

	ok = true
	return ok, vmExpand, err
}
