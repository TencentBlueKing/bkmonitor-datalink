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
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/lucene_parser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
)

const (
	StaticField = "value"

	UUID = "query_uuid"
)

const (
	ConditionEqual       = "eq"
	ConditionNotEqual    = "ne"
	ConditionRegEqual    = "req"
	ConditionNotRegEqual = "nreq"
	ConditionContains    = "contains"
	ConditionNotContains = "ncontains"

	ConditionExisted    = "existed"
	ConditionNotExisted = "nexisted"

	ConditionExact = "exact"
	ConditionGt    = "gt"
	ConditionGte   = "gte"
	ConditionLt    = "lt"
	ConditionLte   = "lte"
)

const (
	DefaultReferenceName = "a"
)

const (
	TypeDate      = "date"
	TypeDateNanos = "date_nanos"
)

type VmCondition string

type FieldAlias map[string]string

type TimeField struct {
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
	Unit string `json:"unit,omitempty"`
}

// Aggregate 聚合方法
type Aggregate struct {
	Name  string `json:"name,omitempty"`
	Field string `json:"field,omitempty"`

	Dimensions []string `json:"dimensions,omitempty"`
	Without    bool     `json:"without,omitempty"`

	Window         time.Duration `json:"window,omitempty"`
	TimeZone       string        `json:"time_zone,omitempty"`
	TimeZoneOffset int64         `json:"time_zone_offset,omitempty"`

	Args []any `json:"args,omitempty"`
}

// OffSetInfo Offset的信息存储，供promql查询转换为influxdb查询语句时使用
type OffSetInfo struct {
	OffSet  time.Duration
	Limit   int
	SOffSet int
	SLimit  int
}

type Aggregates []Aggregate

// Query 查询扩展信息，为后面查询提供定位
type Query struct {
	SourceType string `json:"source_type,omitempty"`
	Password   string `json:"password,omitempty"` // 查询鉴权

	ClusterID string `json:"cluster_id,omitempty"` // 存储 ID

	StorageType string `json:"storage_type,omitempty"` // 存储类型
	SliceID     string `json:"slice_id,omitempty"`     // 切片 ID
	StorageID   string `json:"storage_id,omitempty"`
	StorageName string `json:"storage_name,omitempty"`

	ClusterName string   `json:"cluster_name,omitempty"`
	TagsKey     []string `json:"tags_key,omitempty"`

	DataSource string `json:"data_source,omitempty"`
	DataLabel  string `json:"data_label,omitempty"`
	TableID    string `json:"table_id,omitempty"`

	// vm 的 rt
	VmRt          string `json:"vm_rt,omitempty"`
	CmdbLevelVmRt string `json:"cmdb_level_vm_rt,omitempty"`

	// 兼容 InfluxDB 结构体
	RetentionPolicy string     `json:"retention_policy,omitempty"` // 存储 RP
	DB              string     `json:"db,omitempty"`               // 存储 DB
	Measurement     string     `json:"measurement,omitempty"`      // 存储 Measurement
	MeasurementType string     `json:"measurement_type,omitempty"` // 存储类型
	Field           string     `json:"field,omitempty"`            // 存储 Field
	TimeField       TimeField  `json:"time_field,omitempty"`       // 时间字段
	Timezone        string     `json:"timezone,omitempty"`         // 存储 Timezone
	Fields          []string   `json:"fields,omitempty"`           // 存储命中的 Field 列表，一般情况下为一个，当 Field 为模糊匹配时，解析为多个
	FieldAlias      FieldAlias `json:"field_alias,omitempty"`
	Measurements    []string   `json:"measurements,omitempty"` // 存储命中的 Measurement 列表，一般情况下为一个，当 Measurement 为模糊匹配时，解析为多个
	MetricNames     []string   `json:"metric_names,omitempty"`

	// 用于 promql 查询
	IsHasOr bool `json:"is_has_or,omitempty"` // 标记是否有 or 条件

	Aggregates Aggregates `json:"aggregates,omitempty"` // 聚合方法列表，从内到外排序

	Condition string `json:"condition,omitempty"` // 过滤条件

	// Vm 过滤条件
	VmCondition    VmCondition `json:"vm_condition,omitempty"`
	VmConditionNum int         `json:"vm_condition_num,omitempty"`

	Filters []map[string]string `json:"filters,omitempty"` // 查询中自带查询条件，用于拼接

	OffsetInfo OffSetInfo `json:"offset_info,omitempty"` // limit等偏移量配置

	SegmentedEnable bool `json:"segmented_enable,omitempty"` // 是否开启分段查询

	// 查询扩展
	QueryString string `json:"query_string,omitempty"`
	IsPrefix    bool   `json:"is_prefix,omitempty"`

	// sql 查询
	SQL string `json:"sql,omitempty"`

	AllConditions AllConditions `json:"all_conditions,omitempty"`

	Source []string `json:"source,omitempty"`
	From   int      `json:"from,omitempty"`
	Size   int      `json:"size,omitempty"`

	Scroll            string             `json:"scroll,omitempty"`
	ResultTableOption *ResultTableOption `json:"result_table_option,omitempty"`

	Orders      Orders    `json:"orders,omitempty"`
	NeedAddTime bool      `json:"need_add_time,omitempty"`
	Collapse    *Collapse `json:"collapse,omitempty"`

	DryRun bool `json:"dry_run,omitempty"`
}

func (q *Query) VMExpand() *VmExpand {
	return &VmExpand{
		ResultTableList: []string{q.VmRt},
		MetricFilterCondition: map[string]string{
			DefaultReferenceName: q.VmCondition.String(),
		},
		ClusterName: q.StorageName,
	}
}

func (q *Query) LabelMap() (map[string][]function.LabelMapValue, error) {
	labelMap := make(map[string][]function.LabelMapValue)
	labelCheck := make(map[string]struct{})

	addLabel := func(key string, operator string, values ...string) {
		if len(values) == 0 {
			return
		}

		for _, value := range values {
			checkKey := key + ":" + value + ":" + operator
			if _, ok := labelCheck[checkKey]; !ok {
				labelCheck[checkKey] = struct{}{}
				labelMap[key] = append(labelMap[key], function.LabelMapValue{
					Value:    value,
					Operator: operator,
				})
			}
		}
	}

	for _, condition := range q.AllConditions {
		for _, cond := range condition {
			if cond.Value != nil && len(cond.Value) > 0 {
				// 处理通配符
				if cond.IsWildcard {
					addLabel(cond.DimensionName, ConditionContains, cond.Value...)
				} else {
					switch cond.Operator {
					// 只保留等于和包含的用法，其他类型不用处理
					case ConditionEqual, ConditionExact, ConditionContains:
						addLabel(cond.DimensionName, cond.Operator, cond.Value...)
					}
				}
			}
		}
	}

	if q.QueryString != "" {
		err := lucene_parser.LabelMap(q.QueryString, addLabel)
		if err != nil {
			return nil, err
		}
	}

	return labelMap, nil
}

type HighLight struct {
	MaxAnalyzedOffset int  `json:"max_analyzed_offset,omitempty"`
	Enable            bool `json:"enable,omitempty"`
}

type Collapse struct {
	Field string `json:"field,omitempty"`
}

type Order struct {
	Name string
	Ast  bool
}

type Orders []Order

type AllConditions [][]ConditionField

type QueryList []*Query

type QueryMetric struct {
	QueryList QueryList

	ReferenceName string
	MetricName    string

	IsCount bool // 标记是否为 count 方法
}

// ConditionField 过滤条件的字段描述
type ConditionField struct {
	// DimensionName 过滤字段
	DimensionName string
	// Value 查询值
	Value []string
	// Operator 操作符，包含：eq, ne, erq, nreq, contains, ncontains
	Operator string

	// IsWildcard 是否是通配符
	IsWildcard bool

	// IsPrefix 是否是前缀匹配
	IsPrefix bool

	// IsSuffix 是否是后缀匹配
	IsSuffix bool
}

// TimeAggregation 时间聚合字段
type TimeAggregation struct {
	// Function 时间聚合方法
	Function string
	// Window 聚合周期
	WindowDuration time.Duration

	Without bool
}

type QueryClusterMetric struct {
	MetricName      string
	Aggregates      Aggregates         // 聚合方法列表，从内到外排序
	Conditions      [][]ConditionField // 用户请求的完整过滤条件，来源 structured 定义
	TimeAggregation TimeAggregation
}

type QueryReference map[string][]*QueryMetric

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

func ReplaceVmCondition(condition VmCondition, replaceLabels ReplaceLabels) VmCondition {
	if len(replaceLabels) == 0 {
		return condition
	}

	expr, err := metricsql.Parse(condition.ToMatch())
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

	return VmCondition(cond)
}

// ToJson 通过 tableID 排序，并且返回 json 序列化
func (qMetric *QueryMetric) ToJson(isSort bool) string {
	if isSort {
		sort.SliceStable(qMetric.QueryList, func(i, j int) bool {
			a := qMetric.QueryList[i].TableID
			b := qMetric.QueryList[j].TableID
			return a < b
		})
	}

	s, _ := json.Marshal(qMetric)
	return string(s)
}

func (qRef QueryReference) Count() int {
	var i int
	qRef.Range("", func(qry *Query) {
		i++
	})

	return i
}

// Range 遍历查询列表
func (qRef QueryReference) Range(name string, fn func(qry *Query)) {
	for refName, references := range qRef {
		if name != "" {
			if refName != name {
				continue
			}
		}

		for _, reference := range references {
			if reference == nil {
				continue
			}
			for _, query := range reference.QueryList {
				if query == nil {
					continue
				}

				fn(query)
			}
		}
	}
}

func (qRef QueryReference) GetMaxWindowAndTimezone() (time.Duration, string) {
	var (
		window   time.Duration = 0
		timezone string
	)
	qRef.Range("", func(q *Query) {
		for _, a := range q.Aggregates {
			if a.Window > window {
				window = a.Window
				timezone = a.TimeZone
			}
		}
	})

	return window, timezone
}

// ToVmExpand 判断是否是直查，如果都是 vm 查询的情况下，则使用直查模式
func (qRef QueryReference) ToVmExpand(_ context.Context) (vmExpand *VmExpand) {
	vmClusterNames := set.New[string]()
	vmResultTable := set.New[string]()
	metricFilterCondition := make(map[string]string)

	for referenceName, references := range qRef {
		if len(references) == 0 {
			continue
		}

		// 因为是直查，reference 还需要承担聚合语法生成，所以 vm 不支持同指标的拼接，所以这里只取第一个 reference
		reference := references[0]
		if 0 < len(reference.QueryList) {
			vmConditions := set.New[string]()
			for _, query := range reference.QueryList {
				if query.VmRt == "" {
					continue
				}

				vmResultTable.Add(query.VmRt)
				vmConditions.Add(string(query.VmCondition))
				vmClusterNames.Add(query.StorageName)
			}

			filterCondition := ""
			if vmConditions.Size() > 0 {
				filterCondition = fmt.Sprintf(`%s`, strings.Join(vmConditions.ToArray(), ` or `))
			}

			metricFilterCondition[referenceName] = filterCondition
		}
	}

	if vmResultTable.Size() == 0 {
		return vmExpand
	}

	vmExpand = &VmExpand{
		MetricFilterCondition: metricFilterCondition,
		ResultTableList:       vmResultTable.ToArray(),
	}
	sort.Strings(vmExpand.ResultTableList)

	// 当所有的 vm 集群都一样的时候，才进行传递
	if vmClusterNames.Size() == 1 {
		vmExpand.ClusterName = vmClusterNames.First()
	}

	return vmExpand
}

func (vs VmCondition) String() string {
	return string(vs)
}

func (vs VmCondition) ToMatch() string {
	return fmt.Sprintf("{%s}", vs)
}

// LastAggName 获取最新的聚合函数
func (a Aggregates) LastAggName() string {
	if len(a) == 0 {
		return ""
	}

	return a[len(a)-1].Name
}

func (a Aggregates) Copy() Aggregates {
	aggs := make(Aggregates, len(a))
	for i, agg := range a {
		aggs[i] = Aggregate{
			Name:           agg.Name,
			Field:          agg.Field,
			Dimensions:     append([]string{}, agg.Dimensions...),
			Window:         agg.Window,
			TimeZone:       agg.TimeZone,
			TimeZoneOffset: agg.TimeZoneOffset,
			Args:           append([]any{}, agg.Args...),
		}
	}
	return aggs
}

func (os Orders) SortSliceList(list []map[string]any, fieldType map[string]string) {
	if len(os) == 0 {
		return
	}
	if len(list) == 0 {
		return
	}

	sort.SliceStable(list, func(i, j int) bool {
		for _, o := range os {
			a := list[i][o.Name]
			b := list[j][o.Name]

			if a == b {
				continue
			}

			if ft, ok := fieldType[o.Name]; ok {
				switch ft {
				case TypeDate, TypeDateNanos:
					t1 := function.StringToNanoUnix(cast.ToString(a))
					t2 := function.StringToNanoUnix(cast.ToString(b))

					if t1 > 0 && t2 > 0 {
						if o.Ast {
							return t1 < t2
						} else {
							return t1 > t2
						}
					}
				}
			}

			// 如果是 float 格式则使用 float 进行对比
			f1, f1Err := cast.ToFloat64E(a)
			f2, f2Err := cast.ToFloat64E(b)
			if f1Err == nil && f2Err == nil {
				if o.Ast {
					return f1 < f2
				} else {
					return f1 > f2
				}
			}

			// 最后使用 string 的方式进行排序
			t1 := cast.ToString(a)
			t2 := cast.ToString(b)
			if o.Ast {
				return t1 < t2
			} else {
				return t1 > t2
			}
		}
		return false
	})
}

func (fa FieldAlias) OriginField(f string) string {
	if v, ok := fa[f]; ok {
		return v
	}
	return ""
}

func (fa FieldAlias) AliasName(f string) string {
	for k, v := range fa {
		if v == f {
			return k
		}
	}
	return ""
}
