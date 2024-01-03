// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta1

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dominikbraun/graph"
	"github.com/prometheus/prometheus/model/labels"
	pl "github.com/prometheus/prometheus/promql"
	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

const (
	QueryMaxRouting = 2
	Timeout         = time.Minute
)

var (
	mdl *model
	mtx sync.Mutex
)

func GetModel(ctx context.Context) (cmdb.CMDB, error) {
	var err error
	if mdl == nil {
		mtx.Lock()
		mdl, err = newModel(ctx)
		mtx.Unlock()
	}
	return mdl, err
}

type model struct {
	cfg *Config

	m map[cmdb.Resource]cmdb.Index
	g graph.Graph[string, string]
}

// newModel 初始化
func newModel(ctx context.Context) (*model, error) {
	var (
		err error
		cfg = configData
	)

	// 初始化 graph 存储结构
	g := graph.New(graph.StringHash)

	// 初始化资源 map 配置
	m := make(map[cmdb.Resource]cmdb.Index, len(cfg.Resource))

	// 按照 index 数量倒序，用于判断资源
	sort.SliceStable(cfg.Resource, func(i, j int) bool {
		return len(cfg.Resource[i].Index) > len(cfg.Resource[j].Index)
	})

	for _, r := range cfg.Resource {
		m[r.Name] = r.Index

		if err = g.AddVertex(string(r.Name)); err != nil {
			return nil, fmt.Errorf("add vertex error: %s", err.Error())
		}
	}
	for _, r := range cfg.Relation {
		if len(r.Resources) != 2 {
			return nil, fmt.Errorf("wrong model %+v", r.Resources)
		}

		if err = g.AddEdge(string(r.Resources[0]), string(r.Resources[1])); err != nil {
			return nil, fmt.Errorf("add edge error: %s", err.Error())
		}
	}

	return &model{
		cfg: cfg,

		m: m,
		g: g,
	}, nil
}

func (r *model) resources(ctx context.Context) ([]cmdb.Resource, error) {
	rs := make([]cmdb.Resource, 0, len(r.m))
	for k := range r.m {
		rs = append(rs, k)
	}
	sort.Slice(rs, func(i, j int) bool {
		return rs[i] < rs[j]
	})
	return rs, nil
}

func (r *model) getResourceIndex(ctx context.Context, resource cmdb.Resource) (cmdb.Index, error) {
	if r.m == nil {
		return nil, fmt.Errorf("reation m is nil")
	}

	if v, ok := r.m[resource]; ok {
		return v, nil
	} else {
		return nil, fmt.Errorf("resource is empty %s", resource)
	}
}

func (r *model) getResourceFromMatch(ctx context.Context, matcher cmdb.Matcher) (cmdb.Resource, cmdb.Matcher, error) {
	for _, resource := range r.cfg.Resource {
		if indexMatcher := indexInMather(ctx, resource.Index, matcher); indexMatcher != nil {
			return resource.Name, indexMatcher, nil
		}
	}
	return "", nil, fmt.Errorf("empty resource with %+v", matcher)
}

func (r *model) getPaths(ctx context.Context, source, target cmdb.Resource, matcher cmdb.Matcher) (cmdb.Paths, error) {
	// 获取最短路径
	p, err := graph.ShortestPath(r.g, string(source), string(target))
	if err != nil {
		return nil, fmt.Errorf("%s => %s error: %s", source, target, err)
	}
	path, err := pathParser(p)
	if err != nil {
		return nil, fmt.Errorf("path parser %v error: %s", p, err)
	}
	return cmdb.Paths{path}, nil

	// 暂时不使用全路径
	//allGraphPaths, err := graph.AllPathsBetween(r.g, string(source), string(target))
	//if err != nil {
	//	return nil, err
	//}
	//// 从最短路径开始验证
	//sort.SliceStable(allGraphPaths, func(i, j int) bool {
	//	return len(allGraphPaths[i]) < len(allGraphPaths[j])
	//})
	//
	//allPaths := make(cmdb.Paths, 0, len(allGraphPaths))
	//for _, p := range allGraphPaths {
	//	paths, err := pathParser(p)
	//	if err != nil {
	//		continue
	//	}
	//	allPaths = append(allPaths, paths)
	//}
	//return allPaths, nil
}

func (r *model) queryResourceMatcher(ctx context.Context, lookBackDelta, spaceUid string, step time.Duration, startTs, endTs int64, target cmdb.Resource, matcher cmdb.Matcher, instant bool) (cmdb.Resource, cmdb.Matcher, []cmdb.MatchersWithTimestamp, error) {
	var (
		span oleltrace.Span
		user = metadata.GetUser(ctx)
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "get-resource-matcher")
	if span != nil {
		defer span.End()
	}

	trace.InsertStringIntoSpan("source", user.Source, span)
	trace.InsertStringIntoSpan("username", user.Name, span)
	trace.InsertStringIntoSpan("space-uid", spaceUid, span)
	trace.InsertIntIntoSpan("startTs", int(startTs), span)
	trace.InsertIntIntoSpan("endTs", int(endTs), span)
	trace.InsertStringIntoSpan("step", step.String(), span)
	trace.InsertStringIntoSpan("target", string(target), span)
	trace.InsertStringIntoSpan("matcher", fmt.Sprintf("%v", matcher), span)

	queryMatcher := matcher.Rename()

	trace.InsertStringIntoSpan("query-matcher", fmt.Sprintf("%v", queryMatcher), span)

	source, indexMatcher, err := r.getResourceFromMatch(ctx, queryMatcher)
	if err != nil {
		return source, indexMatcher, nil, fmt.Errorf("get resource error: %s", err)
	}

	if spaceUid == "" {
		return source, indexMatcher, nil, fmt.Errorf("space uid is empty")
	}

	if startTs == 0 || endTs == 0 {
		return source, indexMatcher, nil, fmt.Errorf("timestamp is empty")
	}

	trace.InsertStringIntoSpan("source", string(source), span)
	trace.InsertStringIntoSpan("index-matcher", fmt.Sprintf("%v", indexMatcher), span)

	paths, err := r.getPaths(ctx, source, target, queryMatcher)
	if err != nil {
		return source, indexMatcher, nil, fmt.Errorf("get paths error: %s", err)
	}

	trace.InsertStringIntoSpan("paths", fmt.Sprintf("%v", paths), span)

	var resultMatchers []cmdb.MatchersWithTimestamp
	for _, path := range paths {
		resultMatchers, err = r.doRequest(ctx, lookBackDelta, spaceUid, startTs, endTs, step, path, indexMatcher, instant)
		if err != nil {
			continue
		}

		if len(resultMatchers) > 0 {
			trace.InsertStringIntoSpan("path", fmt.Sprintf("%v", path), span)
			break
		}
	}

	return source, indexMatcher, resultMatchers, err
}

func (r *model) QueryResourceMatcher(ctx context.Context, lookBackDelta, spaceUid string, timestamp int64, target cmdb.Resource, matcher cmdb.Matcher) (cmdb.Resource, cmdb.Matcher, cmdb.Matchers, error) {
	resource, matcher, ret, err := r.queryResourceMatcher(ctx, lookBackDelta, spaceUid, time.Duration(0), timestamp, timestamp, target, matcher, true)
	if err != nil {
		return resource, matcher, nil, err
	}

	return resource, matcher, shimMatcherWithTimestamp(ret), nil
}

func (r *model) QueryResourceMatcherRange(ctx context.Context, lookBackDelta, spaceUid string, step time.Duration, startTs, endTs int64, target cmdb.Resource, matcher cmdb.Matcher) (cmdb.Resource, cmdb.Matcher, []cmdb.MatchersWithTimestamp, error) {
	return r.queryResourceMatcher(ctx, lookBackDelta, spaceUid, step, startTs, endTs, target, matcher, false)
}

func (r *model) doRequest(ctx context.Context, lookBackDeltaStr, spaceUid string, startTs, endTs int64, step time.Duration, path cmdb.Path, matcher cmdb.Matcher, instant bool) ([]cmdb.MatchersWithTimestamp, error) {
	// 按照关联路径遍历查询
	var (
		lookBackDelta time.Duration
		err           error
	)
	if lookBackDeltaStr != "" {
		lookBackDelta, err = time.ParseDuration(lookBackDeltaStr)
		if err != nil {
			return nil, err
		}
	}

	queryTs, err := r.makeQuery(ctx, spaceUid, path, matcher)
	if err != nil {
		return nil, err
	}

	queryReference, err := queryTs.ToQueryReference(ctx)
	if err != nil {
		return nil, err
	}
	metadata.SetQueryReference(ctx, queryReference)

	var instance tsdb.Instance
	ok, vmExpand, err := queryReference.CheckVmQuery(ctx)
	if ok {
		if err != nil {
			return nil, err
		}

		metadata.SetExpand(ctx, vmExpand)
		instance = prometheus.GetInstance(ctx, &metadata.Query{
			StorageID: consul.VictoriaMetricsStorageType,
		})
		if instance == nil {
			err = fmt.Errorf("%s storage get error", consul.VictoriaMetricsStorageType)
			return nil, err
		}
	} else {
		instance = prometheus.NewInstance(ctx, promql.GlobalEngine, &prometheus.QueryRangeStorage{
			QueryMaxRouting: QueryMaxRouting,
			Timeout:         Timeout,
		}, lookBackDelta)
	}

	referenceNameMetric := make(map[string]string, len(queryTs.QueryList))
	referenceNameLabelMatcher := make(map[string][]*labels.Matcher, len(queryTs.QueryList))
	promQL, err := queryTs.ToPromExpr(ctx, referenceNameMetric, referenceNameLabelMatcher)
	if err != nil {
		return nil, fmt.Errorf("query ts to prom expr error: %s", err)
	}

	statement := promQL.String()
	start := time.Unix(startTs, 0)
	end := time.Unix(endTs, 0)

	var matrix pl.Matrix
	var vector pl.Vector
	if instant {
		vector, err = instance.Query(ctx, statement, end)
		matrix = vectorToMatrix(vector)
	} else {
		matrix, err = instance.QueryRange(ctx, statement, start, end, step)
	}
	if err != nil {
		return nil, fmt.Errorf("instance query error: %s", err)
	}

	if len(matrix) == 0 {
		return nil, fmt.Errorf("instance data empty, statement: %s, matcher: %+v", statement, matcher)
	}

	merged := make(map[int64]cmdb.Matchers)
	for _, series := range matrix {
		for _, p := range series.Points {
			lbs := make(cmdb.Matcher, len(series.Metric))
			for _, m := range series.Metric {
				lbs[m.Name] = m.Value
			}
			merged[p.T] = append(merged[p.T], lbs)
		}
	}

	// 按时间戳聚合并排序
	ret := make([]cmdb.MatchersWithTimestamp, 0, len(merged))
	for k, v := range merged {
		ret = append(ret, cmdb.MatchersWithTimestamp{
			Timestamp: k,
			Matchers:  v,
		})
	}

	sort.Slice(ret, func(i, j int) bool {
		return ret[i].Timestamp < ret[j].Timestamp
	})

	return ret, nil
}

func (r *model) getIndexMatcher(ctx context.Context, resource cmdb.Resource, matcher cmdb.Matcher) (cmdb.Matcher, error) {
	index, err := r.getResourceIndex(ctx, resource)
	if len(index) == 0 {
		return nil, fmt.Errorf("resource %s get index empty error %s", resource, err)
	}

	indexMatcher := make(cmdb.Matcher, len(index))
	for _, idx := range index {
		if v, ok := matcher[idx]; ok {
			indexMatcher[idx] = v
		} else {
			return nil, fmt.Errorf("matcher %v have not key %s", matcher, idx)
		}
	}
	return indexMatcher, nil
}

func (r *model) makeQuery(ctx context.Context, spaceUid string, path cmdb.Path, matcher cmdb.Matcher) (*structured.QueryTs, error) {
	const ascii = 97 // a

	queryTs := &structured.QueryTs{
		SpaceUid: spaceUid,
	}

	for i, p := range path {
		if len(p.V) < 2 {
			return nil, fmt.Errorf("path format is wrong %v", p)
		}

		metric := getMetric(p)
		if metric == "" {
			return nil, fmt.Errorf("metric is empty %v", p)
		}

		sourceIndex, err := r.getResourceIndex(ctx, p.V[0])
		if err != nil {
			return nil, err
		}
		targetIndex, err := r.getResourceIndex(ctx, p.V[1])
		if err != nil {
			return nil, err
		}

		onConnect := strings.Join(sourceIndex, ",")
		groupBy := strings.Join(targetIndex, ",")

		ref := string(rune(ascii + i))

		if i == 0 {
			queryTs.QueryList = append(queryTs.QueryList, &structured.Query{
				FieldName:     metric,
				ReferenceName: ref,
				Conditions:    convertMapToConditions(matcher),
			})
			queryTs.MetricMerge = fmt.Sprintf(`(count(%s) by (%s))`, ref, groupBy)
		} else {
			// 如果查询条件在其他 relation 中也存在，也需要补充，比如（bcs_cluster_id）
			includeMatcher := make(cmdb.Matcher)
			for _, index := range targetIndex {
				if v, ok := matcher[index]; ok {
					includeMatcher[index] = v
				}
			}

			queryTs.QueryList = append(queryTs.QueryList, &structured.Query{
				FieldName:     metric,
				ReferenceName: ref,
				Conditions:    convertMapToConditions(includeMatcher),
			})

			queryTs.MetricMerge = fmt.Sprintf(`count(%s and on(%s) %s) by (%s)`, ref, onConnect, queryTs.MetricMerge, groupBy)
		}
	}

	return queryTs, nil
}

func vectorToMatrix(vector pl.Vector) pl.Matrix {
	var matrix pl.Matrix
	for _, sample := range vector {
		matrix = append(matrix, pl.Series{
			Metric: sample.Metric,
			Points: []pl.Point{sample.Point},
		})
	}
	return matrix
}

// shimMatcherWithTimestamp 如果是 instant 查询，则保留一个数据（理论上也只有一个数据）
func shimMatcherWithTimestamp(matchers []cmdb.MatchersWithTimestamp) cmdb.Matchers {
	if len(matchers) == 0 {
		return nil
	}

	pick := matchers[len(matchers)-1]
	return pick.Matchers
}

func getMetric(relation cmdb.Relation) string {
	if len(relation.V) == 2 {
		v := []string{string(relation.V[0]), string(relation.V[1])}
		sort.Strings(v)
		return fmt.Sprintf("%s_relation", strings.Join(v, "_with_"))
	}
	return ""
}

func convertMapToConditions(matcher cmdb.Matcher) structured.Conditions {
	cond := structured.Conditions{}
	for k, v := range matcher {
		cond.FieldList = append(cond.FieldList, structured.ConditionField{
			DimensionName: k,
			Value:         []string{v},
			Operator:      structured.ConditionEqual,
		})
	}

	// 所有条件均为 and 拼接
	for i := 0; i < len(matcher)-1; i++ {
		cond.ConditionList = append(cond.ConditionList, "and")
	}
	return cond
}

func pathParser(p []string) (cmdb.Path, error) {
	if len(p) < 2 {
		return nil, fmt.Errorf("path format is wrong %s", p)
	}

	path := make(cmdb.Path, 0, len(p)-1)
	for i := 0; i < len(p)-1; i++ {
		v := []cmdb.Resource{cmdb.Resource(p[i]), cmdb.Resource(p[i+1])}
		path = append(path, cmdb.Relation{
			V: v,
		})
	}
	return path, nil
}

func indexInMather(ctx context.Context, index cmdb.Index, matcher cmdb.Matcher) cmdb.Matcher {
	indexMatcher := make(cmdb.Matcher)
	for _, i := range index {
		if v, ok := matcher[i]; ok {
			indexMatcher[i] = v
		} else {
			return nil
		}
	}

	return indexMatcher
}
