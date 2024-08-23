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
	pl "github.com/prometheus/prometheus/promql"

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

	// 按照 index 数量倒序，用于判断资源归属
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

// getIndexMatcher 获取该资源过滤条件
func (r *model) getIndexMatcher(ctx context.Context, resource cmdb.Resource, matcher cmdb.Matcher) (cmdb.Matcher, bool, error) {
	var err error
	indexMatcher := make(cmdb.Matcher)
	index, err := r.getResourceIndex(ctx, resource)
	if err != nil {
		return indexMatcher, false, err
	}
	allMatch := true
	for _, i := range index {
		if v, ok := matcher[i]; ok {
			indexMatcher[i] = v
		} else {
			allMatch = false
		}
	}

	return indexMatcher, allMatch, nil
}

// getResourceFromMatch 通过查询条件判断归属哪个资源
func (r *model) getResourceFromMatch(ctx context.Context, matcher cmdb.Matcher) (cmdb.Resource, error) {
	for _, resource := range r.cfg.Resource {
		_, allMatch, err := r.getIndexMatcher(ctx, resource.Name, matcher)
		if err != nil {
			return "", err
		}

		if allMatch {
			return resource.Name, nil
		}
	}

	return "", fmt.Errorf("resource is empty with %+v", matcher)
}

func (r *model) checkPath(graphPath []string, pathResource []cmdb.Resource) bool {
	if len(graphPath) < len(pathResource) {
		return false
	}

	gpm := make(map[string]struct{}, len(graphPath))
	for _, gp := range graphPath {
		gpm[gp] = struct{}{}
	}

	for _, pr := range pathResource {
		if _, ok := gpm[string(pr)]; !ok {
			return false
		}
	}

	return true
}

func (r *model) getPaths(ctx context.Context, source, target cmdb.Resource, pathResource []cmdb.Resource) (cmdb.Paths, error) {

	// 如果不指定经过的路径的话，则使用最短路径
	if len(pathResource) == 0 {
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
	}

	// 暂时不使用全路径
	allGraphPaths, err := graph.AllPathsBetween(r.g, string(source), string(target))
	if err != nil {
		return nil, err
	}
	// 从最短路径开始验证
	sort.SliceStable(allGraphPaths, func(i, j int) bool {
		return len(allGraphPaths[i]) < len(allGraphPaths[j])
	})

	allPaths := make(cmdb.Paths, 0, len(allGraphPaths))
	for _, p := range allGraphPaths {
		if !r.checkPath(p, pathResource) {
			continue
		}

		paths, err := pathParser(p)
		if err != nil {
			continue
		}
		allPaths = append(allPaths, paths)
	}
	return allPaths, nil
}

func (r *model) queryResourceMatcher(ctx context.Context, opt QueryResourceOptions) (cmdb.Resource, cmdb.Matcher, []cmdb.MatchersWithTimestamp, error) {
	var (
		err  error
		user = metadata.GetUser(ctx)
	)

	ctx, span := trace.NewSpan(ctx, "get-resource-matcher")
	defer span.End(&err)

	span.Set("source", user.Source)
	span.Set("username", user.Name)
	span.Set("space-uid", opt.SpaceUid)
	span.Set("startTs", opt.StartTs)
	span.Set("endTs", opt.EndTs)
	span.Set("step", opt.Step.String())
	span.Set("source", opt.Source)
	span.Set("target", opt.Target)
	span.Set("matcher", fmt.Sprintf("%v", opt.Matcher))
	span.Set("target", opt.PathResource)

	queryMatcher := opt.Matcher.Rename()

	span.Set("query-matcher", fmt.Sprintf("%v", queryMatcher))

	if opt.Source == "" {
		opt.Source, err = r.getResourceFromMatch(ctx, queryMatcher)
		if err != nil {
			return opt.Source, queryMatcher, nil, fmt.Errorf("get resource error: %s", err)
		}
	}

	indexMatcher, _, err := r.getIndexMatcher(ctx, opt.Source, queryMatcher)
	if err != nil {
		return opt.Source, queryMatcher, nil, fmt.Errorf("get index matcher error: %s", err)
	}

	if opt.SpaceUid == "" {
		return opt.Source, indexMatcher, nil, fmt.Errorf("space uid is empty")
	}

	if opt.StartTs == 0 || opt.EndTs == 0 {
		return opt.Source, indexMatcher, nil, fmt.Errorf("timestamp is empty")
	}

	span.Set("source", string(opt.Source))
	span.Set("index-matcher", fmt.Sprintf("%v", indexMatcher))

	paths, err := r.getPaths(ctx, opt.Source, opt.Target, opt.PathResource)
	if err != nil {
		return opt.Source, indexMatcher, nil, fmt.Errorf("get paths error: %s", err)
	}

	span.Set("paths", fmt.Sprintf("%v", paths))

	var resultMatchers []cmdb.MatchersWithTimestamp
	for _, path := range paths {
		resultMatchers, err = r.doRequest(ctx, opt.LookBackDelta, opt.SpaceUid, opt.StartTs, opt.EndTs, opt.Step, path, indexMatcher, opt.Instant)
		if err != nil {
			continue
		}

		if len(resultMatchers) > 0 {
			span.Set("path", fmt.Sprintf("%v", path))
			break
		}
	}

	return opt.Source, indexMatcher, resultMatchers, err
}

type QueryResourceOptions struct {
	LookBackDelta string
	SpaceUid      string
	Step          time.Duration
	StartTs       int64
	EndTs         int64
	Target        cmdb.Resource
	Source        cmdb.Resource
	Matcher       cmdb.Matcher
	PathResource  []cmdb.Resource
	Instant       bool
}

func (r *model) QueryResourceMatcher(ctx context.Context, lookBackDelta, spaceUid string, timestamp int64, target, source cmdb.Resource, matcher cmdb.Matcher, pathResource []cmdb.Resource) (cmdb.Resource, cmdb.Matcher, cmdb.Matchers, error) {
	opt := QueryResourceOptions{
		LookBackDelta: lookBackDelta,
		SpaceUid:      spaceUid,
		Step:          time.Duration(0),
		StartTs:       timestamp,
		EndTs:         timestamp,
		Source:        source,
		Target:        target,
		Matcher:       matcher,
		PathResource:  pathResource,
		Instant:       true,
	}
	resource, matcher, ret, err := r.queryResourceMatcher(ctx, opt)
	if err != nil {
		return resource, matcher, nil, err
	}

	return resource, matcher, shimMatcherWithTimestamp(ret), nil
}

func (r *model) QueryResourceMatcherRange(ctx context.Context, lookBackDelta, spaceUid string, step time.Duration, startTs, endTs int64, target, source cmdb.Resource, matcher cmdb.Matcher, pathResource []cmdb.Resource) (cmdb.Resource, cmdb.Matcher, []cmdb.MatchersWithTimestamp, error) {
	opt := QueryResourceOptions{
		LookBackDelta: lookBackDelta,
		SpaceUid:      spaceUid,
		Step:          step,
		StartTs:       startTs,
		EndTs:         endTs,
		Source:        source,
		Target:        target,
		Matcher:       matcher,
		PathResource:  pathResource,
		Instant:       true,
	}
	return r.queryResourceMatcher(ctx, opt)
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
		instance = prometheus.GetTsDbInstance(ctx, &metadata.Query{
			StorageType: consul.VictoriaMetricsStorageType,
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

	promQL, err := queryTs.ToPromExpr(ctx, nil)
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
		checkString := instance.Check(ctx, statement, start, end, step)
		return nil, fmt.Errorf("instance data empty, check: %s", checkString)
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
