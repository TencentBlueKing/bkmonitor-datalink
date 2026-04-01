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
	"sync"
	"time"

	"github.com/dominikbraun/graph"
	"github.com/pkg/errors"
	"golang.org/x/sync/singleflight"
	pl "github.com/prometheus/prometheus/promql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

const (
	QueryMaxRouting = 2
	Timeout         = time.Minute
)

var (
	mdl      *model
	mtx      sync.Mutex
	provider relation.SchemaProvider

	// reloadGroup ensures that concurrent schema change events coalesce into
	// a single ReloadConfig call, preventing redundant model rebuilds under
	// notification storms from Redis Pub/Sub.
	reloadGroup singleflight.Group
)

// InitSchemaProvider 初始化 SchemaProvider，应在服务启动时（Redis 初始化之后）调用
// 设置后，GetModel 会优先从 SchemaProvider 获取配置，获取失败则回退到硬编码 configData
func InitSchemaProvider(p relation.SchemaProvider) {
	mtx.Lock()
	defer mtx.Unlock()
	provider = p
	// 重置 model，下次 GetModel 调用时会用新的 provider 重建
	mdl = nil
	
	// Register callback for schema changes if provider supports it
	if provider != nil {
		if err := provider.Subscribe(onSchemaChange); err != nil {
			log.Warnf(context.Background(), "failed to subscribe to schema changes: %v", err)
		}
	}
}

// onSchemaChange is called when schema (resource or relation definitions) changes.
// It uses singleflight to coalesce concurrent reload requests so that rapid-fire
// Pub/Sub events (e.g. batch schema updates) result in only one model rebuild.
func onSchemaChange(kind, namespace string) {
	ctx := context.Background()
	log.Infof(ctx, "schema change detected: kind=%s, namespace=%s", kind, namespace)

	_, err, shared := reloadGroup.Do("reload", func() (interface{}, error) {
		return nil, ReloadConfig(ctx)
	})
	if err != nil {
		log.Warnf(ctx, "failed to reload v1beta1 model on schema change: %v", err)
	}
	if shared {
		log.Debugf(ctx, "schema reload was shared with concurrent request")
	}
}

func GetModel(ctx context.Context) (cmdb.CMDB, error) {
	var err error
	if mdl == nil {
		mtx.Lock()
		if mdl == nil {
			mdl, err = newModel(ctx)
		}
		mtx.Unlock()
	}
	return mdl, err
}

type model struct {
	cfg *Config

	g graph.Graph[string, string]
}

// newModel 初始化
// 优先从 SchemaProvider 获取配置，失败则回退到硬编码 configData
func newModel(ctx context.Context) (*model, error) {
	var (
		err          error
		cfg          *Config
		configSource string
	)

	ctx, span := trace.NewSpan(ctx, "v1beta1-new-model")
	defer span.End(&err)

	// 尝试从 SchemaProvider 获取动态配置
	if provider != nil {
		adapter := NewConfigAdapter(provider)
		cfg, err = adapter.GetConfig(ctx, "")
		if err != nil {
			log.Warnf(ctx, "failed to get config from SchemaProvider, falling back to hardcoded config: %v", err)
			span.Set("schema-provider-error", err.Error())
			cfg = nil
			err = nil // 清除错误，允许回退
		} else {
			switch provider.(type) {
			case *relation.RedisProvider:
				configSource = "redis"
			case *relation.StaticSchemaProvider:
				configSource = "static"
			default:
				configSource = "schema_provider"
			}
			log.Infof(ctx, "v1beta1 model loaded from SchemaProvider: %d resources, %d relations",
				len(cfg.Resource), len(cfg.Relation))
		}
	}

	// 回退到硬编码配置
	if cfg == nil {
		cfg = configData
		configSource = "hardcoded"
		log.Infof(ctx, "v1beta1 model using hardcoded config: %d resources, %d relations",
			len(cfg.Resource), len(cfg.Relation))
	}

	// 记录 trace 信息：配置来源、资源/关联数量和名称
	span.Set("config-source", configSource)
	span.Set("config-resource-count", len(cfg.Resource))
	span.Set("config-relation-count", len(cfg.Relation))

	resourceNames := make([]string, 0, len(cfg.Resource))
	for _, r := range cfg.Resource {
		resourceNames = append(resourceNames, string(r.Name))
	}
	span.Set("config-resource-names", resourceNames)

	relationNames := make([]string, 0, len(cfg.Relation))
	for _, r := range cfg.Relation {
		if len(r.Resources) == 2 {
			relationNames = append(relationNames, fmt.Sprintf("%s->%s", r.Resources[0], r.Resources[1]))
		}
	}
	span.Set("config-relation-names", relationNames)

	// 更新全局 resourceConfig 映射（供 ResourcesIndex/ResourcesInfo/AllResources 使用）
	updateResourceConfig(cfg)

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

		g: g,
	}, nil
}

func (r *model) resources(ctx context.Context) ([]cmdb.Resource, error) {
	rs := make([]cmdb.Resource, 0, len(AllResources()))
	for k := range AllResources() {
		rs = append(rs, k)
	}
	sort.Slice(rs, func(i, j int) bool {
		return rs[i] < rs[j]
	})
	return rs, nil
}

// getIndexMatcher 获取该资源过滤条件
func (r *model) getIndexMatcher(ctx context.Context, resource cmdb.Resource, matcher cmdb.Matcher) (cmdb.Matcher, bool, error) {
	var err error
	indexMatcher := make(cmdb.Matcher)
	index := ResourcesIndex(resource)
	if len(index) == 0 {
		err = fmt.Errorf("index is empty with %+v", resource)
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
	// 如果不传则判断为命中
	if len(pathResource) == 0 {
		return true
	}

	// 如果长度为 1，且为空，则直接判断直连路径，长度为 2
	if len(pathResource) == 1 && pathResource[0] == "" && len(graphPath) == 2 {
		return true
	}

	// 如果指定的路径大于需要判断的路径则完全无法命中
	if len(pathResource) > len(graphPath) {
		return false
	}

	startIndex := -1
	for idx, sp := range graphPath {
		if sp == string(pathResource[0]) {
			startIndex = idx
			break
		}
	}

	if startIndex < 0 {
		return false
	}

	for idx, pr := range pathResource {
		if string(pr) != graphPath[startIndex+idx] {
			return false
		}
	}

	return true
}

func (r *model) getPaths(ctx context.Context, source, target cmdb.Resource, pathResource []cmdb.Resource) ([][]string, error) {
	// 暂时不使用全路径
	allGraphPaths, err := graph.AllPathsBetween(r.g, string(source), string(target))
	if err != nil {
		return nil, err
	}
	// 从最短路径开始验证
	sort.SliceStable(allGraphPaths, func(i, j int) bool {
		return len(allGraphPaths[i]) < len(allGraphPaths[j])
	})

	// 兼容原来的节点屏蔽功能，因为没有指定路径，原路径 pod -> node -> system, 最短路径可能会命中：pod -> apm_service_instance -> system，所以需要多路径查询匹配
	paths := make([][]string, 0)
	for _, p := range allGraphPaths {
		if r.checkPath(p, pathResource) {
			paths = append(paths, p)
		}
	}

	if len(paths) == 0 {
		return nil, fmt.Errorf("empty paths with %s => %s through %v", source, target, pathResource)
	}

	return paths, nil
}

func (r *model) queryResourceMatcher(ctx context.Context, opt QueryResourceOptions) (source cmdb.Resource, sourceInfo cmdb.Matcher, hitPath []string, target cmdb.Resource, ts []cmdb.MatchersWithTimestamp, err error) {
	user := metadata.GetUser(ctx)

	ctx, span := trace.NewSpan(ctx, "get-resource-indexMatcher")
	defer span.End(&err)

	span.Set("query-source-type", user.Source)
	span.Set("query-username", user.Name)
	span.Set("query-space-uid", opt.SpaceUid)
	span.Set("query-start-ts", opt.Start)
	span.Set("query-end-ts", opt.End)
	span.Set("query-step", opt.Step)
	span.Set("query-resource", opt.Source)
	span.Set("query-target-resource", opt.Target)
	span.Set("query-index-matcher", opt.IndexMatcher)
	span.Set("query-path-resource", opt.PathResource)

	opt.IndexMatcher = opt.IndexMatcher.Rename()
	span.Set("query-renamed-index-matcher", opt.IndexMatcher)

	if opt.Source == "" {
		opt.Source, err = r.getResourceFromMatch(ctx, opt.IndexMatcher)
		if err != nil {
			err = errors.WithMessage(err, "get resource error")
			return source, sourceInfo, hitPath, target, ts, err
		}
	}

	// 如果 target 为空，则使用 source 作为 target，用于 info 数据展示
	if opt.Target == "" {
		opt.Target = opt.Source
	}

	if opt.SpaceUid == "" {
		err = errors.New("space uid is empty")
		return source, sourceInfo, hitPath, target, ts, err
	}

	if opt.Start == "" || opt.End == "" {
		err = errors.New("timestamp is empty")
		return source, sourceInfo, hitPath, target, ts, err
	}

	// query-resource already set above

	source = opt.Source
	target = opt.Target
	sourceInfo, _, err = r.getIndexMatcher(ctx, opt.Source, opt.IndexMatcher)
	if err != nil {
		err = errors.WithMessagef(err, "get index matcher error")
		return source, sourceInfo, hitPath, target, ts, err
	}

	paths, err := r.getPaths(ctx, opt.Source, opt.Target, opt.PathResource)
	if err != nil {
		err = errors.WithMessagef(err, "get path error")
		return source, sourceInfo, hitPath, target, ts, err
	}

	span.Set("query-relation-paths", paths)
	metadata.GetQueryParams(ctx).SetIsSkipK8s(true)

	var errorMessage []string

	for _, path := range paths {
		reqTs, reqErr := r.doRequest(ctx, path, opt)
		if reqErr != nil {
			errorMessage = append(errorMessage, fmt.Sprintf("path [%v] do request error: %s", path, reqErr))
			continue
		}

		hitPath = path
		if len(reqTs) > 0 {
			ts = reqTs
			break
		}
	}

	if len(ts) == 0 {
		metadata.NewMessage(
			metadata.MsgQueryRelation,
			"%s",
			"查询不到数据",
		).Warn(ctx)
	}

	span.Set("query-hit-path", hitPath)
	return source, sourceInfo, hitPath, target, ts, err
}

type QueryResourceOptions struct {
	LookBackDelta string
	SpaceUid      string
	Step          string
	Start         string
	End           string
	Target        cmdb.Resource
	Source        cmdb.Resource

	IndexMatcher  cmdb.Matcher
	ExpandMatcher cmdb.Matcher

	PathResource []cmdb.Resource

	ExpandShow bool

	Instant bool
}

func (r *model) QueryResourceMatcher(ctx context.Context, lookBackDelta, spaceUid string, timestamp string, target, source cmdb.Resource, indexMatcher, expandMatcher cmdb.Matcher, expandShow bool, pathResource []cmdb.Resource) (cmdb.Resource, cmdb.Matcher, []string, cmdb.Resource, cmdb.Matchers, error) {
	opt := QueryResourceOptions{
		LookBackDelta: lookBackDelta,
		SpaceUid:      spaceUid,
		Start:         timestamp,
		End:           timestamp,
		Source:        source,
		Target:        target,
		IndexMatcher:  indexMatcher,
		ExpandMatcher: expandMatcher,
		PathResource:  pathResource,
		ExpandShow:    expandShow,
		Instant:       true,
	}
	source, sourceInfo, path, target, ret, err := r.queryResourceMatcher(ctx, opt)
	if err != nil {
		return "", nil, path, "", nil, err
	}

	return source, sourceInfo, path, target, shimMatcherWithTimestamp(ret), nil
}

func (r *model) QueryResourceMatcherRange(ctx context.Context, lookBackDelta, spaceUid string, step string, start, end string, target, source cmdb.Resource, indexMatcher, expandMatcher cmdb.Matcher, expandShow bool, pathResource []cmdb.Resource) (cmdb.Resource, cmdb.Matcher, []string, cmdb.Resource, []cmdb.MatchersWithTimestamp, error) {
	opt := QueryResourceOptions{
		LookBackDelta: lookBackDelta,
		SpaceUid:      spaceUid,
		Step:          step,
		Start:         start,
		End:           end,
		Source:        source,
		Target:        target,
		IndexMatcher:  indexMatcher,
		ExpandMatcher: expandMatcher,
		ExpandShow:    expandShow,
		PathResource:  pathResource,
		Instant:       false,
	}
	return r.queryResourceMatcher(ctx, opt)
}

func (r *model) doRequest(ctx context.Context, path []string, opt QueryResourceOptions) ([]cmdb.MatchersWithTimestamp, error) {
	// 按照关联路径遍历查询
	var (
		lookBackDelta time.Duration
		err           error
	)

	ctx, span := trace.NewSpan(ctx, "query-do-request")
	defer span.End(&err)

	indexMatcher, _, err := r.getIndexMatcher(ctx, opt.Source, opt.IndexMatcher)
	if err != nil {
		return nil, errors.WithMessagef(err, "get index indexMatcher error")
	}

	span.Set("query-resource-options", opt)

	if opt.LookBackDelta != "" {
		lookBackDelta, err = time.ParseDuration(opt.LookBackDelta)
		if err != nil {
			return nil, err
		}
	}

	queryMaker := &QueryFactory{
		Path:   path,
		Source: opt.Source,

		Start: opt.Start,
		End:   opt.End,
		Step:  opt.Step,

		IndexMatcher:  indexMatcher,
		ExpandMatcher: opt.ExpandMatcher,

		Target:     opt.Target,
		ExpandShow: opt.ExpandShow,
	}

	queryTs, err := queryMaker.MakeQueryTs()
	if err != nil {
		return nil, err
	}

	queryReference, err := queryTs.ToQueryReference(ctx)
	if err != nil {
		return nil, err
	}

	var instance tsdb.Instance

	qb := metadata.GetQueryParams(ctx)

	if qb.IsDirectQuery() {
		vmExpand := query.ToVmExpand(ctx, queryReference)

		metadata.SetExpand(ctx, vmExpand)
		instance = prometheus.GetTsDbInstance(ctx, &metadata.Query{
			StorageType: metadata.VictoriaMetricsStorageType,
		})
		if instance == nil {
			err = fmt.Errorf("%s storage get error", metadata.VictoriaMetricsStorageType)
			return nil, err
		}
	} else {
		instance = prometheus.NewInstance(ctx, promql.GlobalEngine, &prometheus.QueryRangeStorage{
			QueryMaxRouting: QueryMaxRouting,
			Timeout:         Timeout,
		}, lookBackDelta, QueryMaxRouting)
	}

	realPromQL, err := queryTs.ToPromQL(ctx)
	if err == nil {
		span.Set("query-promql", realPromQL)
	}

	promQL, err := queryTs.ToPromExpr(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("query ts to prom expr error: %s", err)
	}

	statement := promQL.String()

	var matrix pl.Matrix
	var vector pl.Vector
	if opt.Instant {
		vector, err = instance.DirectQuery(ctx, statement, qb.End)
		matrix = vectorToMatrix(vector)
	} else {
		matrix, _, err = instance.DirectQueryRange(ctx, statement, qb.AlignStart, qb.End, qb.Step)
	}
	if err != nil {
		return nil, fmt.Errorf("instance query error: %s", err)
	}

	if len(matrix) == 0 {
		metadata.NewMessage(
			metadata.MsgQueryRelation,
			"%s",
			"查询不到数据",
		).Warn(ctx)
		return nil, nil
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

// ReloadConfig 重新加载配置
// 当 Redis 数据变更时，可以调用此方法刷新 Model
func ReloadConfig(ctx context.Context) error {
	mtx.Lock()
	defer mtx.Unlock()

	if provider == nil {
		return fmt.Errorf("schema provider not initialized")
	}

	newMdl, err := newModel(ctx)
	if err != nil {
		return fmt.Errorf("failed to create new model: %w", err)
	}

	mdl = newMdl
	log.Infof(ctx, "v1beta1 model reloaded successfully")
	return nil
}

