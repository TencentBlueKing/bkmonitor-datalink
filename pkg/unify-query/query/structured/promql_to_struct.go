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
	"fmt"
	"math"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

const (
	SumOverTime   = "sum_over_time"
	CountOverTime = "count_over_time"
)

// refMgr
type refMgr struct {
	count int
	char  string
}

// Next
func (rm *refMgr) Next() string {
	// 讲道理不会出现这种情况 26 个不同指标求算术表达式 ???
	if rm.count >= 26 {
		s := fmt.Sprintf("z%d", rm.count-26)
		rm.count++
		return s
	}

	s := rm.char[rm.count]
	rm.count++

	return string(s)
}

// structParser
type structParser struct {
	q string
	//promql     []string
	promqlByte []byte
	ref        *refMgr
	expr       parser.Expr
	nodes      []parser.Node
	vecIdx     []int
	vecGroups  []vecGroup
}

// ParseOption
type ParseOption struct {
	Offset           time.Duration // 更改的偏移量
	IsOnlyMetricName bool          // bkmonitor:db:measurement:metric -> metric
}

// NewStructParser
func NewStructParser(q string) *structParser {
	return &structParser{q: q, ref: &refMgr{char: "abcdefghijklmnopqrstuvwxyz"}}
}

// String
func (sp *structParser) String() string {
	return sp.expr.String()
}

// ParseNew 把 promql 解析为结构体
func (sp *structParser) ParseNew() (CombinedQueryParams, error) {
	var err error
	sp.expr, err = parser.ParseExpr(sp.q)
	if err != nil {
		return CombinedQueryParams{}, err
	}

	//sp.promql = strings.Split(sp.q, "")
	sp.promqlByte = []byte(sp.q)

	sp.inspect()
	return sp.parseNew()
}

// ToggleCountAndSum 切换指标的 sum_over_time <=> count_over_time
func (sp *structParser) ToggleCountAndSum(id, funcName string) error {
	var countAndSum = map[string]string{
		CountOverTime: SumOverTime,
		SumOverTime:   CountOverTime,
	}
	if toFuncName, ok := countAndSum[funcName]; ok {
		for _, vec := range sp.vecGroups {
			if vec.ID == id {
				for _, node := range vec.Nodes {
					switch e := node.(type) {
					case *parser.Call:
						if e.Func.Name == funcName {
							e.Func = parser.MustGetFunction(toFuncName)
						}
					}
				}
			}
		}
	} else {
		return fmt.Errorf("%s is not support toggle count and sum", funcName)
	}

	return nil
}

// UpdateMetricName 更新指标名
func (sp *structParser) UpdateMetricName(metricNameMap map[string]string, labelMatchersMap map[string][]*labels.Matcher) {
	parser.Inspect(sp.expr, func(node parser.Node, _ []parser.Node) error {
		if node != nil {
			// 记录所有非空 Node 用于后面分析 Vector 分组情况
			sp.nodes = append(sp.nodes, node)
			idx := len(sp.nodes) - 1

			switch e := node.(type) {
			// 一个 vecGroup 里有且仅有一个 *parser.VectorSelector Node
			case *parser.VectorSelector:
				sp.vecIdx = append(sp.vecIdx, idx)
				// bkmonitor:db:measurement:metric / metric 等 -> metricName

				referenceName := ""
				for i, label := range e.LabelMatchers {
					if label.Name != labels.MetricName {
						continue
					}
					referenceName = label.Value
					if m, ok := metricNameMap[referenceName]; ok {
						e.LabelMatchers[i].Value = m
						e.Name = m
					} else {
						return fmt.Errorf("metrics (%s) is not match in %+v", referenceName, metricNameMap)
					}
					break
				}

				if referenceName != "" {
					if l, ok := labelMatchersMap[referenceName]; ok {
						e.LabelMatchers = append(e.LabelMatchers, l...)
					}
				}

			}
		}
		return nil
	})
}

// UpdatePromql 根据opts对promql的Expr中的node节点进行修改
func (sp *structParser) UpdatePromql(opts *ParseOption) {
	var window time.Duration
	parser.Inspect(sp.expr, func(node parser.Node, _ []parser.Node) error {
		if node != nil {
			// 记录所有非空 Node 用于后面分析 Vector 分组情况
			sp.nodes = append(sp.nodes, node)
			idx := len(sp.nodes) - 1

			switch e := node.(type) {
			// 一个 vecGroup 里有且仅有一个 *parser.VectorSelector Node
			case *parser.VectorSelector:
				sp.vecIdx = append(sp.vecIdx, idx)

				// bkmonitor:db:measurement:metric / metric 等 -> metric
				if opts.IsOnlyMetricName {
					for i, label := range e.LabelMatchers {
						if label.Name != labels.MetricName {
							continue
						}
						route, err := MakeRouteFromMetricName(label.Value)
						if err != nil {
							return err
						}
						e.LabelMatchers[i].Value = route.MetricName()
						e.Name = route.MetricName()
						break
					}
				}

				// 如果不需要偏移则直接返回
				// 和influxdb对齐，增加偏移量
				if opts.Offset == 0 || window == 0 {
					return nil
				}
				window = 0

				// 如果window!=0, 整体时间向右偏移 (offset - 1ns)
				targetOffset := -opts.Offset + time.Nanosecond

				if e.OriginalOffset < 0 {
					// 时间戳向后平移，查询后面的数据
					e.OriginalOffset -= targetOffset
				} else {
					// 时间戳向前平移，查询前面的数据
					e.OriginalOffset += targetOffset
				}

			case *parser.MatrixSelector:
				window = e.Range
			}
		}
		return nil
	})
}

// parse promql to struct
func (sp *structParser) parseNew() (CombinedQueryParams, error) {
	err := sp.splitVecGroups()
	if err != nil {
		return CombinedQueryParams{}, err
	}
	paramLst := make([]*QueryParams, 0)

	for _, group := range sp.vecGroups {
		params := &QueryParams{}
		var (
			window     time.Duration
			isSubQuery bool
			step       string
		)
		for _, node := range group.Nodes {
			switch e := node.(type) {
			// 一个 vecGroup 里有且仅有一个 *parser.VectorSelector Node
			case *parser.VectorSelector:
				params, err = parseVectorToQueryParams(e, params, group.ID)
				if err != nil {
					return CombinedQueryParams{}, err
				}
			case *parser.MatrixSelector:
				window = e.Range
			case *parser.SubqueryExpr:
				window = e.Range
				params.Offset = e.Offset.String()
				var offset string
				if e.OriginalOffset < 0 {
					offset = (-e.OriginalOffset).String()
					params.OffsetForward = true
				} else {
					offset = e.OriginalOffset.String()
				}
				// 无 offset 偏移就置空 不用赋值了
				if offset != "0s" {
					params.Offset = offset
				}

				// @-modifier
				params.Timestamp = e.Timestamp
				params.StartOrEnd = e.StartOrEnd
				params.VectorOffset = e.Offset

				isSubQuery = true
				step = e.Step.String()
			case *parser.Call:
				// 判断是否存在 matrix，是则写入到 timeAggregation
				var (
					callType parser.ValueType
					position int
				)

				// 获取指标的位置
				for index, argType := range e.Func.ArgTypes {
					switch argType {
					// matrix 或者 vector 类型则获取他的位置，以及记录类型
					case parser.ValueTypeMatrix, parser.ValueTypeVector:
						callType = argType
						position = index
					}
				}

				// 获取参数，参数的长度有可能会大于等于 argTypes，所以要单独循环获取
				vargsList := make([]interface{}, 0)
				for _, arg := range e.Args {
					// 其他类型判断是否是常量参数，是则加入到函数参数列表里面
					switch at := arg.(type) {
					case *parser.NumberLiteral:
						vargsList = append(vargsList, at.Val)
					case *parser.StringLiteral:
						vargsList = append(vargsList, at.Val)
					default:
						continue
					}
				}

				if callType == parser.ValueTypeMatrix {
					// 如果是 matrix 类型，一定要有时间
					if window == 0 {
						return CombinedQueryParams{}, fmt.Errorf("%s is not matrix type, because window is %d", e.Func.Name, window)
					}

					timeAggregation := TimeAggregation{
						Function:   e.Func.Name,
						Window:     Window(window.String()),
						Position:   position,
						IsSubQuery: isSubQuery,
						Step:       step,
					}
					if len(vargsList) > 0 {
						timeAggregation.VargsList = vargsList
					}

					// 如果是 matrix 类型，则需要写入到 timeAggregation 里
					params.TimeAggregation = timeAggregation
				} else {
					// 如果是 vector 类型，则需要写入到 aggregateMethodList 里
					aggregateMethod := AggregateMethod{
						Method:   e.Func.Name,
						Position: position,
					}
					if len(vargsList) > 0 {
						aggregateMethod.VArgsList = vargsList
					}
					params.AggregateMethodList = append(params.AggregateMethodList, aggregateMethod)
				}
			case *parser.AggregateExpr:
				method := convertMethod(e.Op)
				if method == "" {
					return CombinedQueryParams{}, fmt.Errorf("aggregate expr op: %d is not exist", e.Op)
				}
				vargsList := make([]interface{}, 1)
				aggregateMethod := AggregateMethod{
					Method:     method,
					Dimensions: e.Grouping,
					Without:    e.Without,
				}

				// 获取参数
				if e.Param != nil {
					switch v := e.Param.(type) {
					case *parser.NumberLiteral:
						vargsList[0] = v.Val
					case *parser.StringLiteral:
						vargsList[0] = v.Val
					default:
						return CombinedQueryParams{}, fmt.Errorf("aggregate expr type: %T is not exists", v)
					}
					aggregateMethod.VArgsList = vargsList
				}

				params.AggregateMethodList = append(params.AggregateMethodList, aggregateMethod)
			}
		}

		paramLst = append(paramLst, params)
	}

	var (
		metricMerge []byte
		start       = 0
		end         = len(sp.promqlByte)
	)
	for _, vec := range sp.vecGroups {
		left := sp.promqlByte[start:vec.StartPos]

		metricMerge = append(metricMerge, left...)
		metricMerge = append(metricMerge, vec.ID...)
		start = vec.EndPos
	}
	if end > start {
		metricMerge = append(metricMerge, sp.promqlByte[start:end]...)
	}

	ret := CombinedQueryParams{
		QueryList:   paramLst,
		MetricMerge: MetricMerge(metricMerge),
	}

	return ret, nil
}

// inspect 使用深度优先遍历语法树 并记录 VectorSelector 的索引位置
// 每个 VectorSelector 可以看做是一棵树的不为空的叶子节点
func (sp *structParser) inspect() {
	parser.Inspect(sp.expr, func(node parser.Node, _ []parser.Node) error {
		if node != nil {
			// 记录所有非空 Node 用于后面分析 Vector 分组情况
			sp.nodes = append(sp.nodes, node)
			idx := len(sp.nodes) - 1

			if _, ok := node.(*parser.VectorSelector); ok {
				sp.vecIdx = append(sp.vecIdx, idx)
			}
		}
		return nil
	})
}

// vecGroup
type vecGroup struct {
	ID       string
	Name     string
	Nodes    []parser.Node
	StartPos int
	EndPos   int
}

// isGroupMember
func (sp *structParser) isGroupMember(node parser.Node) bool {
	switch node.(type) {
	case *parser.VectorSelector:
		return true
	case *parser.Call:
		return true
	case *parser.AggregateExpr:
		return true
	case *parser.MatrixSelector:
		return true
	case *parser.SubqueryExpr:
		return true
	default:
		return false
	}
}

// splitVecGroups 切分 vectorSelector 分组 即叶子节点的上层被哪些 Node 嵌套住
func (sp *structParser) splitVecGroups() error {
	vecGroups := make([]vecGroup, 0)
	preVec := 0

	for _, idx := range sp.vecIdx {
		group := make([]parser.Node, 0)
		start, end := math.MaxInt64, math.MinInt64

		// [idx: preVec] 的闭区间
		for j := idx; j >= preVec; j-- {
			node := sp.nodes[j]
			// 如果是二元操作符的话就不做进一步解析了
			if _, ok := node.(*parser.BinaryExpr); ok {
				break
			}

			// 只有部分的 Node 类型会属于一个 VectorSelector 分组
			if sp.isGroupMember(node) {
				if int(node.PositionRange().Start) > start || int(node.PositionRange().End) < end {
					continue
				}
			} else {
				continue
			}

			// 记录整个分组的 PositionRange
			if int(node.PositionRange().Start) < start {
				start = int(node.PositionRange().Start)
			}
			if int(node.PositionRange().End) > end {
				end = int(node.PositionRange().End)
			}

			var l []int
			var r []int
			for i := node.PositionRange().Start; i < node.PositionRange().End; i++ {
				if sp.q[i] == '(' {
					l = append(l, int(i))
				}
				if sp.q[i] == ')' {
					r = append(r, int(i))
				}
			}

			if len(r) > 0 && len(l) > 0 && len(r) != len(l) {
				oldStr := fmt.Sprintf("promql to struct change postion：[%d:%d] %s", start, end, sp.q[start:end])
				if len(r) > len(l) {
					end = r[len(l)-1] + 1
				} else {
					start = l[len(r)-1]
				}
				log.Debugf(context.TODO(), "%s => [%d:%d] %s\n", oldStr, start, end, sp.q[start:end])
			}

			group = append(group, node)
		}

		preVec = idx + 1
		vecGroups = append(vecGroups, vecGroup{
			ID:       sp.ref.Next(),
			Nodes:    group,
			StartPos: start,
			EndPos:   end,
		})
	}

	sp.vecGroups = vecGroups
	return nil
}

// parseVectorToQueryParams
func parseVectorToQueryParams(
	e *parser.VectorSelector, params *QueryParams, referenceName string,
) (*QueryParams, error) {
	if e == nil {
		return params, nil
	}
	if params == nil {
		params = new(QueryParams)
	}
	conds := make([]ConditionField, 0)
	route, err := MakeRouteFromLBMatchOrMetricName(e.LabelMatchers)
	if err != nil {
		return params, err
	}

	for _, label := range e.LabelMatchers {
		if label.Name == labels.MetricName {
			continue
		}

		// bk_database, bk_measurement 2个系统 label 需要过滤
		if label.Name == bkDatabaseLabelName || label.Name == bkMeasurementLabelName {
			continue
		}

		cond := ConditionField{
			DimensionName: label.Name,
			Value:         []string{label.Value},
		}

		op := convertOp(label.Type)
		if op == "" {
			return params, fmt.Errorf("failed to decode the '%s' operation symbol", label.Type)
		}
		cond.Operator = op
		conds = append(conds, cond)
	}

	// 匹配 Conditions 组合条件
	cl := make([]string, 0)
	for i := 0; i < len(conds)-1; i++ {
		cl = append(cl, "and")
	}

	params.DataSource = route.DataSource()
	params.DB = Bucket(route.DB())
	params.TableID = route.TableID()
	params.ReferenceName = ReferenceName(referenceName)
	params.FieldName = FieldName(route.MetricName())
	params.Conditions = Conditions{
		FieldList:     conds,
		ConditionList: cl,
	}

	var offset string
	if e.OriginalOffset < 0 {
		offset = (-e.OriginalOffset).String()
		params.OffsetForward = true
	} else {
		offset = e.OriginalOffset.String()
	}
	// 无 offset 偏移就置空 不用赋值了
	if offset != "0s" {
		params.Offset = offset
	}

	// @-modifier
	params.Timestamp = e.Timestamp
	params.StartOrEnd = e.StartOrEnd
	params.VectorOffset = e.Offset

	return params, nil
}

// convertOp
func convertOp(op labels.MatchType) string {
	switch op {
	case labels.MatchEqual:
		return ConditionEqual
	case labels.MatchNotEqual:
		return ConditionNotEqual
	case labels.MatchRegexp:
		return ConditionRegEqual
	case labels.MatchNotRegexp:
		return ConditionNotRegEqual
	default:
		return ""
	}
}

// convertMethod
func convertMethod(t parser.ItemType) string {
	switch t {
	case parser.COUNT:
		return CountAggName
	case parser.MAX:
		return MaxAggName
	case parser.MIN:
		return MinAggName
	case parser.AVG:
		return MeanAggName
	case parser.SUM:
		return SumAggName
	case parser.BOTTOMK:
		return BottomKAggName
	case parser.TOPK:
		return TopkAggName
	case parser.QUANTILE:
		return QuantileAggName
	case parser.GROUP:
		return GroupAggName
	case parser.STDDEV:
		return StddevAggName
	case parser.STDVAR:
		return StdvarAggName
	case parser.COUNT_VALUES:
		return CountValuesAggName
	}

	return ""
}
