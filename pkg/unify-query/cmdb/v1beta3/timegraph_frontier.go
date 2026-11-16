// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/pkg/errors"
	pl "github.com/prometheus/prometheus/promql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// 每个选择器最多包含 64 组完整身份，避免条件规模随图中的实例数无限增长。
const timeGraphFrontierBatchSize = 64

// timeGraphWideFrontierThreshold 是从按身份分批切换到宽读取的身份数。分批是顺序
// 查询，耗时随身份数线性增长；超过这个规模后一次宽读取的总代价更低。
const timeGraphWideFrontierThreshold = 2 * timeGraphFrontierBatchSize

// timeGraphBackendResponseBytes 是单次后端响应解压后的分批边界，不是能力上限：
// 超过它的读取先按身份对半拆分，单个身份再按时间对半拆分，总读取量可以大于它。
const timeGraphBackendResponseBytes int64 = 16 * 1024 * 1024

type timeGraphFrontier map[cmdb.Resource]map[string]cmdb.Matcher

func (q *TimeGraph) completeFrontierIdentity(resource cmdb.Resource, info cmdb.Matcher) bool {
	fields := q.nodeBuilder.resourceIndexes(resource)
	if len(fields) == 0 {
		return false
	}
	for _, field := range fields {
		if info[field] == "" {
			return false
		}
	}
	return true
}

// frontierAnchorFields 把关系上的标签映射到锚点资源的主键字段。
func (q *TimeGraph) frontierAnchorFields(relation cmdb.Relation, targetAnchor bool) (cmdb.Resource, map[string]string) {
	resource := relation.V[0]
	sourcePrefix, targetPrefix := q.relationEndpointPrefixes(relation, relation.V[0], relation.V[1])
	prefix := sourcePrefix
	if targetAnchor {
		resource, prefix = relation.V[1], targetPrefix
	}
	anchorFields := make(map[string]string)
	for _, field := range q.nodeBuilder.resourceIndexes(resource) {
		label := field
		if relation.Category == string(RelationCategoryDynamic) {
			label = prefix + field
		}
		anchorFields[label] = field
	}
	return resource, anchorFields
}

// frontierQuery 即使从目标端观察关系，也保留原关系方向。
// 每个 OR 分组包含一组完整联合主键和端点条件；逐字段 IN 会错误匹配交叉组合。
func (loader *timeGraphMatrixLoader) frontierQuery(ctx context.Context, space string, relation cmdb.Relation, targetAnchor bool, identities []cmdb.Matcher) (*structured.QueryTs, error) {
	q := loader.graph
	ts, err := q.MakeQueryTsWithWindow(ctx, space, nil, loader.start, loader.end, loader.step, loader.lookBack, relation)
	if err != nil || ts == nil {
		return ts, err
	}
	resource, anchorFields := q.frontierAnchorFields(relation, targetAnchor)
	base := ts.QueryList[0].Conditions.FieldList
	conditions := structured.Conditions{}
	for _, identity := range identities {
		if !q.completeFrontierIdentity(resource, identity) {
			return nil, errors.New("frontier identity has incomplete primary fields")
		}
		for i, field := range base {
			if len(conditions.FieldList) > 0 {
				connector := structured.ConditionAnd
				if i == 0 {
					connector = structured.ConditionOr
				}
				conditions.ConditionList = append(conditions.ConditionList, connector)
			}
			if primary, ok := anchorFields[field.DimensionName]; ok {
				field.Operator = structured.ConditionEqual
				field.Value = []string{identity[primary]}
			}
			conditions.FieldList = append(conditions.FieldList, field)
		}
	}
	ts.QueryList[0].Conditions = conditions
	return ts, nil
}

// frontierScopeQuery 用一个查询覆盖整个前沿：锚点一端取值不超过一批的主键字段收窄到
// 取值列表，其余字段不收窄。逐字段列表是联合主键的超集，按路径求可达的调用方必须按身份
// 过滤结果；多出的序列仍是同一关系的真实边。narrowed 为假时查询等同整条关系的宽读取。
func (loader *timeGraphMatrixLoader) frontierScopeQuery(ctx context.Context, space string, relation cmdb.Relation, targetAnchor bool, identities []cmdb.Matcher) (ts *structured.QueryTs, narrowed bool, err error) {
	q := loader.graph
	ts, err = q.MakeQueryTsWithWindow(ctx, space, nil, loader.start, loader.end, loader.step, loader.lookBack, relation)
	if err != nil || ts == nil {
		return ts, false, err
	}
	_, anchorFields := q.frontierAnchorFields(relation, targetAnchor)
	values := make(map[string]map[string]struct{}, len(anchorFields))
	for _, primary := range anchorFields {
		values[primary] = make(map[string]struct{})
	}
	for _, identity := range identities {
		for primary, distinct := range values {
			if len(distinct) <= timeGraphFrontierBatchSize {
				distinct[identity[primary]] = struct{}{}
			}
		}
	}
	fields := ts.QueryList[0].Conditions.FieldList
	for i, field := range fields {
		primary, ok := anchorFields[field.DimensionName]
		if !ok || len(values[primary]) > timeGraphFrontierBatchSize {
			continue
		}
		list := make([]string, 0, len(values[primary]))
		for value := range values[primary] {
			list = append(list, value)
		}
		sort.Strings(list)
		field.Operator = structured.ConditionContains
		field.Value = list
		fields[i] = field
		narrowed = true
	}
	return ts, narrowed, nil
}

// loadFrontier 读取以 identities 为锚点一端的关系边。规模超过阈值时先尝试一次宽读取，响应超过
// 分批边界再退回分批：边界限制的是单次响应，分批读取的总量仍可以大于它。本请求出现过超过边界的
// 宽读取后，说明它的读取范围可能都很大：之后的宽读取先按同一范围计数，估算超过分批边界就不发，
// 直接分批。没有出现过超限的请求不计数，读取与此前相同。
// whole 表示成功的宽读取没有收窄任何字段，已读入整条关系。
func (loader *timeGraphMatrixLoader) loadFrontier(ctx context.Context, space string, relation cmdb.Relation, targetAnchor bool, identities []cmdb.Matcher, apply func(context.Context, pl.Matrix) error, queries *int) (whole bool, err error) {
	state := loader.frontierState(relation, targetAnchor)
	if len(identities) > timeGraphWideFrontierThreshold {
		ts, narrowed, err := loader.frontierScopeQuery(ctx, space, relation, targetAnchor, identities)
		if err != nil {
			return false, err
		}
		series, counted, fits := 0, false, ts != nil
		if fits && loader.frontierCounting {
			if series, counted, err = loader.frontierScopeCount(ctx, ts); err != nil {
				return false, err
			}
			if counted && state.estimate(series, loader.frontierPoints()) > timeGraphBackendResponseBytes*3/4 {
				loader.frontierWideSkips++
				fits = false
			}
		}
		if fits {
			*queries = *queries + 1
			// 独立子查询：被拒收的读取留下的质量状态不能影响替代它的分批读取。
			bounded := metadata.WithBackendResponseLimit(frontierSubqueryContext(ctx), timeGraphBackendResponseBytes)
			matrix, _, err := loader.query(bounded, ts)
			if err == nil {
				state.observeSeries(matrix)
				if err := apply(ctx, matrix); err != nil {
					return false, err
				}
				return !narrowed, nil
			}
			if !isBackendResponseTooLarge(err) {
				return false, err
			}
			// 超过边界：之后的宽读取先计数。这次没有计数时补一次，按“边界 ÷ 序列数”校准这条关系
			// 每条序列的字节下界，同样规模的范围不再先读一次。
			loader.frontierWideOverflows++
			loader.frontierCounting = true
			if !counted {
				if series, counted, err = loader.frontierScopeCount(ctx, ts); err != nil {
					return false, err
				}
			}
			if counted {
				state.underestimated(series)
			}
		}
	}
	for offset := 0; offset < len(identities); {
		end := min(offset+state.batch, len(identities))
		observed, err := loader.loadFrontierBatchObserved(ctx, space, relation, targetAnchor, identities[offset:end], apply, queries)
		if err != nil {
			return false, err
		}
		state.seriesBytes = max(state.seriesBytes, observed.seriesBytes)
		state.adjustBatch(end-offset, observed)
		offset = end
	}
	return false, nil
}

// frontierScopeCount 按关系的维度去重计数宽读取范围内整个窗口出现过的序列。计数只返回一个数，
// 代价远小于一次超过边界后被丢弃的读取；计数失败时 counted 为假，调用方按没有计数处理。
func (loader *timeGraphMatrixLoader) frontierScopeCount(ctx context.Context, ts *structured.QueryTs) (series int, counted bool, err error) {
	probe := loader.frontierScopeProbe(ts)
	if probe == nil {
		return 0, false, nil
	}
	// 计数与其他不可复用的读取一样清空复用槽：之后的宽读取或分批读取都与计数前那次读取不同。
	probeCtx := newTimeGraphSubqueryContext(ctx)
	metadata.GetQueryParams(probeCtx).SetIsSkipK8s(true)
	probeCtx = withTimeGraphQueryStage(probeCtx, "frontier-probe")
	loader.frontierProbeCount++
	matrix, _, probeErr := loader.queryWithQuality(probeCtx, probe, false)
	if probeErr != nil {
		return 0, false, ctx.Err()
	}
	if len(matrix) > 0 && len(matrix[0].Points) > 0 {
		series = int(matrix[0].Points[len(matrix[0].Points)-1].V)
	}
	return series, true, nil
}

// frontierScopeProbe 把宽读取改写成计数：窗口覆盖所有评估点各自的回看窗口，在最后一个网格点
// 按关系维度去重后计数，与宽读取返回的序列一一对应。
func (loader *timeGraphMatrixLoader) frontierScopeProbe(ts *structured.QueryTs) *structured.QueryTs {
	if ts == nil || len(ts.QueryList) != 1 || len(ts.QueryList[0].AggregateMethodList) != 1 {
		return nil
	}
	last := loader.end
	if loader.step > 0 && loader.end.After(loader.start) {
		last = loader.start.Add(loader.end.Sub(loader.start) / loader.step * loader.step)
	}
	window := last.Sub(loader.start) + loader.lookBack
	if window <= 0 {
		window = loader.step
	}
	query := *ts.QueryList[0]
	query.TimeAggregation.Window = timeGraphQueryWindow(window)
	query.AggregateMethodList = structured.AggregateMethodList{ts.QueryList[0].AggregateMethodList[0], {Method: structured.COUNT}}
	probe := *ts
	probe.QueryList = []*structured.Query{&query}
	probe.Start = strconv.FormatInt(last.Unix(), 10)
	probe.End = probe.Start
	return &probe
}

// frontierPoints 是一次读取返回的每条序列的评估点数。
func (loader *timeGraphMatrixLoader) frontierPoints() int {
	if loader.step <= 0 || !loader.end.After(loader.start) {
		return 1
	}
	return int(loader.end.Sub(loader.start)/loader.step) + 1
}

// timeGraphResponsePointBytes 与 timeGraphResponseSeriesBytes 按实际后端响应拟合：每个评估点
// （时间戳与取值）约 18 字节，每条序列的标签与结构在还没读到该关系时按 120 字节估。
const (
	timeGraphResponsePointBytes  = 18
	timeGraphResponseSeriesBytes = 120
)

// timeGraphFrontierBatchMax 与 timeGraphFrontierConditionBytes 限制一批的身份数与过滤条件字节：
// 过滤条件按结果表各写一份，一批的条件字节取在后端可接受的请求体之内。
const (
	timeGraphFrontierBatchMax       = 1024
	timeGraphFrontierConditionBytes = 512 * 1024
)

// frontierReadState 是本请求内某条关系从某一端读取时学到的规模，只影响读取的形状。
type frontierReadState struct {
	// seriesBytes 是已读到的每条序列标签与结构字节的最大值；floor 是一次估算偏低的宽读取
	// 给出的每条序列字节下界（边界除以序列数）。
	seriesBytes int
	floor       int64
	// batch 是下一批的身份数。
	batch int
}

func (loader *timeGraphMatrixLoader) frontierState(relation cmdb.Relation, targetAnchor bool) *frontierReadState {
	key := fmt.Sprintf("%+v|%t", timeGraphRelationKeyFor(relation), targetAnchor)
	if loader.frontierStates == nil {
		loader.frontierStates = map[string]*frontierReadState{}
	}
	state, ok := loader.frontierStates[key]
	if !ok {
		state = &frontierReadState{batch: timeGraphFrontierBatchSize}
		loader.frontierStates[key] = state
	}
	return state
}

func (state *frontierReadState) estimate(series, points int) int64 {
	perSeries := int64(max(state.seriesBytes, timeGraphResponseSeriesBytes) + timeGraphResponsePointBytes*points)
	return int64(series) * max(perSeries, state.floor)
}

// observeSeries 记录读到的每条序列标签与结构的字节（按 JSON 编码估算）。
func (state *frontierReadState) observeSeries(matrix pl.Matrix) {
	if len(matrix) == 0 {
		return
	}
	state.seriesBytes = max(state.seriesBytes, timeGraphResponseLabelBytes(matrix)/len(matrix))
}

func (state *frontierReadState) underestimated(series int) {
	if series > 0 {
		state.floor = max(state.floor, timeGraphBackendResponseBytes/int64(series)+1)
	}
}

// adjustBatch 按上一批的读取结果调整下一批的身份数：超过边界时减半；满批且响应不到边界的四分之一时
// 翻倍；不满一批（前沿已读完）不改批大小。都不超过条件字节与身份数的上限，每步最多翻倍或减半，
// 同一请求内收敛到能读通的最大批。
func (state *frontierReadState) adjustBatch(identities int, observed frontierBatchObservation) {
	if identities <= 0 {
		return
	}
	next := state.batch
	switch {
	case observed.tooLarge:
		next = identities / 2
	case identities >= state.batch && observed.responseBytes*4 < timeGraphBackendResponseBytes:
		next = 2 * identities
	}
	if observed.conditionBytes > 0 {
		next = min(next, int(int64(identities)*timeGraphFrontierConditionBytes/int64(observed.conditionBytes)))
	}
	state.batch = max(1, min(next, timeGraphFrontierBatchMax))
}

// frontierBatchObservation 记录一批读取的结果：单次响应的最大估计字节、每条序列标签与结构的最大字节、
// 最外层读取的过滤条件字节，以及是否因超过边界拆分过。
type frontierBatchObservation struct {
	responseBytes  int64
	seriesBytes    int
	conditionBytes int
	tooLarge       bool
}

// timeGraphResponseLabelBytes 按 JSON 编码估算 Matrix 中全部序列的标签与结构字节。
func timeGraphResponseLabelBytes(matrix pl.Matrix) int {
	total := 0
	for _, series := range matrix {
		total += 30
		for _, label := range series.Metric {
			total += len(label.Name) + len(label.Value) + 6
		}
	}
	return total
}

// timeGraphResponseBytes 按 JSON 编码估算 Matrix 的响应字节。
func timeGraphResponseBytes(matrix pl.Matrix) int64 {
	total := int64(timeGraphResponseLabelBytes(matrix))
	for _, series := range matrix {
		total += int64(timeGraphResponsePointBytes * len(series.Points))
	}
	return total
}

// timeGraphConditionBytes 是最近一次准备好的直查过滤条件字节。
func timeGraphConditionBytes(ctx context.Context) int {
	expand := metadata.GetExpand(ctx)
	if expand == nil {
		return 0
	}
	total := 0
	for _, condition := range expand.MetricFilterCondition {
		total += len(condition)
	}
	return total
}

// frontierSubqueryContext 给一次前沿读取独立的 metadata，保留当前查询阶段。
func frontierSubqueryContext(ctx context.Context) context.Context {
	child := newTimeGraphSubqueryContext(ctx)
	metadata.GetQueryParams(child).SetIsSkipK8s(true)
	return withTimeGraphQueryStage(child, timeGraphQueryStage(ctx))
}

// isBackendResponseTooLarge 判断一次读取能否通过拆分重读解决；已拆到底的终态错误不再拆分。
func isBackendResponseTooLarge(err error) bool {
	var terminal *UnsplittableResponseError
	if errors.As(err, &terminal) {
		return false
	}
	var limitErr *ResultLimitError
	return errors.Is(err, metadata.ErrBackendResponseTooLarge) || (errors.As(err, &limitErr) && limitErr.Reason == "max_response_bytes")
}

// loadFrontierBatch 遇到过大响应时先拆分身份组，避免每个时间片重复读取整批候选。
// 单个身份再由 queryAndApply 按时间对半拆分。
func (loader *timeGraphMatrixLoader) loadFrontierBatch(ctx context.Context, space string, relation cmdb.Relation, targetAnchor bool, identities []cmdb.Matcher, apply func(context.Context, pl.Matrix) error, queries *int) error {
	_, err := loader.loadFrontierBatchObserved(ctx, space, relation, targetAnchor, identities, apply, queries)
	return err
}

func (loader *timeGraphMatrixLoader) loadFrontierBatchObserved(ctx context.Context, space string, relation cmdb.Relation, targetAnchor bool, identities []cmdb.Matcher, apply func(context.Context, pl.Matrix) error, queries *int) (observed frontierBatchObservation, err error) {
	if err := ctx.Err(); err != nil {
		return observed, err
	}
	ts, err := loader.frontierQuery(ctx, space, relation, targetAnchor, identities)
	if err != nil {
		return observed, err
	}
	*queries = *queries + 1
	if len(identities) == 1 {
		return observed, loader.queryAndApply(ctx, ts, apply)
	}
	matrix, _, err := loader.query(ctx, ts)
	observed.conditionBytes = timeGraphConditionBytes(ctx)
	if err == nil {
		observed.responseBytes = timeGraphResponseBytes(matrix)
		if len(matrix) > 0 {
			observed.seriesBytes = timeGraphResponseLabelBytes(matrix) / len(matrix)
		}
		return observed, apply(ctx, matrix)
	}
	if !isBackendResponseTooLarge(err) {
		return observed, err
	}
	observed.tooLarge = true
	middle := len(identities) / 2
	for _, half := range [][]cmdb.Matcher{identities[:middle], identities[middle:]} {
		part, err := loader.loadFrontierBatchObserved(frontierSubqueryContext(ctx), space, relation, targetAnchor, half, apply, queries)
		if err != nil {
			return observed, err
		}
		observed.responseBytes = max(observed.responseBytes, part.responseBytes)
		observed.seriesBytes = max(observed.seriesBytes, part.seriesBytes)
	}
	return observed, nil
}
