// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"testing"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

// 本文件的期望取自内置定义（default_config.go 的 is_belongs_to 与 from/to）和接口约定（docs/api/timegraph.md 的 direction、relation_groups、sources），
// 不取自被测代码的输出；多选择器的答案以单选择器（取数路径不变）的并集和整条读取为参照。

func builtinBelongsTo() map[string]*relation.RelationDefinition {
	result := map[string]*relation.RelationDefinition{}
	for _, definition := range relation.DefaultRelationDefinitions() {
		if definition.IsBelongsTo {
			result[definition.Name] = definition
		}
	}
	return result
}

func builtinRelationNames(keep func(*relation.RelationDefinition) bool) []string {
	var names []string
	for _, definition := range relation.DefaultRelationDefinitions() {
		if keep(definition) {
			names = append(names, definition.Name)
		}
	}
	sort.Strings(names)
	return names
}

func topologyRequestModel(backend *hopFetchBackend) *Model {
	return &Model{schemaProvider: NewStaticSchemaProvider(), timeGraphQueryReference: selectorQueryReference, timeGraphVMQueryWithPartial: backend.query}
}

// topologyRequestResult 用同一后端取快照和紧凑格式，返回快照与两种格式按身份规范化后的结果。
func topologyRequestResult(t *testing.T, backend *hopFetchBackend, request cmdb.SharedTopologyQuery) ([]cmdb.SharedTopologySnapshot, []string, []string) {
	t.Helper()
	model := topologyRequestModel(backend)
	request.ResponseFormat = ""
	result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
	require.NoError(t, err)
	request.ResponseFormat = cmdb.CompactTopologyFormat
	compact, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
	require.NoError(t, err)
	decoded := decodeCompactForTest(t, compact.Compact)
	return result.Snapshots, canonicalTopologyForTest(t, result.Snapshots), canonicalTopologyForTest(t, decoded)
}

func topologyRangeRequest(frames []int64, hops int, sources ...cmdb.TopologySource) cmdb.SharedTopologyQuery {
	return cmdb.SharedTopologyQuery{
		SpaceUID: "space", Sources: sources, MaxHops: hops,
		StartTime: frames[0] / 1000, EndTime: frames[len(frames)-1] / 1000, Step: "60s",
	}
}

func podSource(pod string) cmdb.TopologySource {
	return cmdb.TopologySource{SourceType: "pod", SourceInfo: cmdb.Matcher{"bcs_cluster_id": "cluster-a", "namespace": "ns-a", "pod": pod}}
}

// nodeKeysByFrame 返回每帧的节点身份，供与单选择器结果的并集比较。
func nodeKeysByFrame(snapshots []cmdb.SharedTopologySnapshot) []map[string]bool {
	result := make([]map[string]bool, len(snapshots))
	for i, snapshot := range snapshots {
		result[i] = map[string]bool{}
		for _, node := range snapshot.Nodes {
			keys := make([]string, 0, len(node.Dimensions))
			for key, value := range node.Dimensions {
				keys = append(keys, key+"="+value)
			}
			sort.Strings(keys)
			result[i][string(node.ResourceType)+"{"+strings.Join(keys, ",")+"}"] = true
		}
	}
	return result
}

func resourceTypesOf(snapshots []cmdb.SharedTopologySnapshot) map[cmdb.Resource]bool {
	result := map[cmdb.Resource]bool{}
	for _, snapshot := range snapshots {
		for _, node := range snapshot.Nodes {
			result[node.ResourceType] = true
		}
	}
	return result
}

// requireBelongEdgesChildToParent 检查归属关系的边都从子指向父、方向标签为 outbound，且每对节点只有一条。
func requireBelongEdgesChildToParent(t *testing.T, snapshots []cmdb.SharedTopologySnapshot) {
	t.Helper()
	belongs := builtinBelongsTo()
	for _, snapshot := range snapshots {
		types := map[uint64]cmdb.Resource{}
		for _, node := range snapshot.Nodes {
			types[node.ID] = node.ResourceType
		}
		pairs := map[string]int{}
		for _, edge := range snapshot.Edges {
			definition, ok := belongs[edge.RelationType]
			if !ok {
				continue
			}
			require.Equal(t, cmdb.Resource(definition.FromResource), types[edge.Source], "%s 的边从子出发", edge.RelationType)
			require.Equal(t, cmdb.Resource(definition.ToResource), types[edge.Target], "%s 的边指向父", edge.RelationType)
			require.Equal(t, "outbound", edge.Direction)
			pair := fmt.Sprintf("%s/%d/%d", edge.RelationType, min(edge.Source, edge.Target), max(edge.Source, edge.Target))
			pairs[pair]++
			require.Equal(t, 1, pairs[pair], "%s 每对节点只出一条边", edge.RelationType)
		}
	}
}

// direction 作用于归属关系（子 → 父为 out）与动态关系（from → to 为 out），其余静态关系双向。
// 从 pod-1 出发一跳：父是 node、replicaset；子是 container；pod_to_pod 的出边到 pod-2，入边来自 pod-0。
func TestTopologyBelongToFollowsDirection(t *testing.T) {
	frames := hopFetchFrames()
	data := defaultSchemaHopFixture(frames)
	pods := func(snapshots []cmdb.SharedTopologySnapshot) []string {
		set := map[string]bool{}
		for _, snapshot := range snapshots {
			for _, node := range snapshot.Nodes {
				if node.ResourceType == "pod" {
					set[node.Dimensions["pod"]] = true
				}
			}
		}
		var result []string
		for pod := range set {
			result = append(result, pod)
		}
		sort.Strings(result)
		return result
	}
	for _, tc := range []struct {
		direction string
		types     []cmdb.Resource
		pods      []string
	}{
		{direction: "out", types: []cmdb.Resource{"node", "pod", "replicaset"}, pods: []string{"pod-1", "pod-2"}},
		{direction: "in", types: []cmdb.Resource{"container", "pod"}, pods: []string{"pod-0", "pod-1"}},
		{direction: "both", types: []cmdb.Resource{"container", "node", "pod", "replicaset"}, pods: []string{"pod-0", "pod-1", "pod-2"}},
		{direction: "", types: []cmdb.Resource{"container", "node", "pod", "replicaset"}, pods: []string{"pod-0", "pod-1", "pod-2"}},
	} {
		t.Run("direction-"+tc.direction, func(t *testing.T) {
			request := topologyRangeRequest(frames, 1, podSource("pod-1"))
			request.Direction = tc.direction
			snapshots, canonical, compact := topologyRequestResult(t, &hopFetchBackend{t: t, data: data}, request)
			require.Equal(t, canonical, compact)
			var types []cmdb.Resource
			for resource := range resourceTypesOf(snapshots) {
				types = append(types, resource)
			}
			sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
			require.Equal(t, tc.types, types)
			require.Equal(t, tc.pods, pods(snapshots))
			requireBelongEdgesChildToParent(t, snapshots)
			// 紧凑格式还原后的边与快照相同，同样每对节点一条。
			model := topologyRequestModel(&hopFetchBackend{t: t, data: data})
			request.ResponseFormat = cmdb.CompactTopologyFormat
			result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
			require.NoError(t, err)
			requireBelongEdgesChildToParent(t, decodeCompactForTest(t, result.Compact))
		})
	}
}

// direction 只接受 out、in、both（空为 both）；旧的 outbound、inbound 不再接受。
func TestTopologyDirectionValues(t *testing.T) {
	frames := hopFetchFrames()
	model := topologyRequestModel(&hopFetchBackend{t: t, data: defaultSchemaHopFixture(frames)})
	for _, value := range []string{"outbound", "inbound", "OUT"} {
		request := topologyRangeRequest(frames, 1, podSource("pod-1"))
		request.Direction = value
		_, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
		require.ErrorContains(t, err, "unsupported topology direction")
	}
}

// 遍历视图决定读哪些关系：归属关系与动态关系按 direction 只保留一侧视图（子 → 父、调用方 → 被调用方为 out），
// 其余静态关系不分方向，两侧视图都保留。期望由内置定义的 from/to、category 与 is_belongs_to 推出。
func TestTopologyRelationViewsFollowDirection(t *testing.T) {
	model := &Model{schemaProvider: NewStaticSchemaProvider()}
	view := func(name, source, target string, direction TraversalDirection) string {
		return strings.Join([]string{name, source, target, string(direction)}, "|")
	}
	for _, direction := range []TraversalDirection{DirectionOutbound, DirectionInbound, DirectionBoth} {
		t.Run(string(direction), func(t *testing.T) {
			var want []string
			for _, definition := range relation.DefaultRelationDefinitions() {
				outbound := view(definition.Name, definition.FromResource, definition.ToResource, DirectionOutbound)
				inbound := view(definition.Name, definition.ToResource, definition.FromResource, DirectionInbound)
				if definition.IsBelongsTo || definition.Category == string(relation.RelationCategoryDynamic) {
					if direction != DirectionInbound {
						want = append(want, outbound)
					}
					if direction != DirectionOutbound {
						want = append(want, inbound)
					}
					continue
				}
				want = append(want, outbound)
				if !definition.IsDirectional {
					want = append(want, inbound)
				}
			}
			var got []string
			for _, item := range model.sharedTopologyRelations("space", nil, nil, nil, direction) {
				got = append(got, view(item.RelationType, string(item.V[0]), string(item.V[1]), TraversalDirection(item.Direction)))
			}
			require.ElementsMatch(t, want, got)
		})
	}
}

// relation_groups 是固定枚举 BELONG_TO、SERVICE_CALL：未知取值按名字拒绝，不当作空组返回空图；
// 合法取值（含两组同时给出、重复给出）照常查询。
func TestTopologyRelationGroupValues(t *testing.T) {
	frames := hopFetchFrames()
	model := topologyRequestModel(&hopFetchBackend{t: t, data: defaultSchemaHopFixture(frames)})
	for _, tc := range []struct {
		groups  []string
		invalid string
	}{
		{groups: []string{"NOT_A_GROUP"}, invalid: "NOT_A_GROUP"},
		{groups: []string{"belong_to"}, invalid: "belong_to"},
		{groups: []string{"BELONGS_TO"}, invalid: "BELONGS_TO"},
		{groups: []string{""}, invalid: ""},
		{groups: []string{RelationGroupBelongTo, "SERVICE-CALL"}, invalid: "SERVICE-CALL"},
	} {
		request := topologyRangeRequest(frames, 1, podSource("pod-1"))
		request.RelationGroups = tc.groups
		_, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
		require.ErrorContains(t, err, fmt.Sprintf("unsupported relation group %q", tc.invalid), "groups %v", tc.groups)
	}
	for _, groups := range [][]string{
		{RelationGroupBelongTo},
		{RelationGroupServiceCall},
		{RelationGroupBelongTo, RelationGroupServiceCall},
		{RelationGroupBelongTo, RelationGroupBelongTo},
	} {
		request := topologyRangeRequest(frames, 1, podSource("pod-1"))
		request.RelationGroups = groups
		_, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
		require.NoError(t, err, "groups %v", groups)
	}
}

// 关系组由 Schema 推导，BELONG_TO 是静态且 is_belongs_to 的关系，SERVICE_CALL 是动态关系。
// 参照是把组展开成 allowed_relation_types 的同一请求；组与 allowed_relation_types 取交集。
func TestTopologyRelationGroups(t *testing.T) {
	frames := hopFetchFrames()
	data := defaultSchemaHopFixture(frames)
	belongTo := builtinRelationNames(func(d *relation.RelationDefinition) bool { return d.IsBelongsTo })
	serviceCall := builtinRelationNames(func(d *relation.RelationDefinition) bool {
		return d.Category == string(relation.RelationCategoryDynamic)
	})
	require.Len(t, belongTo, 17)
	require.Len(t, serviceCall, 6)
	for _, tc := range []struct {
		name    string
		groups  []string
		allowed []string
		want    []string
	}{
		{name: "belong-to", groups: []string{RelationGroupBelongTo}, want: belongTo},
		{name: "service-call", groups: []string{RelationGroupServiceCall}, want: serviceCall},
		{name: "both-groups", groups: []string{RelationGroupBelongTo, RelationGroupServiceCall}, want: append(append([]string(nil), belongTo...), serviceCall...)},
		{name: "intersection", groups: []string{RelationGroupBelongTo}, allowed: []string{"node_with_pod", "pod_to_pod"}, want: []string{"node_with_pod"}},
	} {
		for _, hops := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/H%d", tc.name, hops), func(t *testing.T) {
				request := topologyRangeRequest(frames, hops, podSource("pod-1"))
				request.RelationGroups = tc.groups
				request.AllowedRelationTypes = tc.allowed
				snapshots, got, gotCompact := topologyRequestResult(t, &hopFetchBackend{t: t, data: data}, request)
				oracle := topologyRangeRequest(frames, hops, podSource("pod-1"))
				oracle.AllowedRelationTypes = tc.want
				_, want, wantCompact := topologyRequestResult(t, &hopFetchBackend{t: t, data: data}, oracle)
				require.Equal(t, want, got)
				require.Equal(t, wantCompact, gotCompact)
				allowed := map[string]bool{}
				for _, name := range tc.want {
					allowed[name] = true
				}
				for _, snapshot := range snapshots {
					for _, edge := range snapshot.Edges {
						require.True(t, allowed[edge.RelationType], "edge %s is outside the selected relations", edge.RelationType)
					}
				}
			})
		}
	}
}

// 原始输入规格：每项 1–20 个选择器；超过时在取数之前按 max_topology_sources 拒绝。
func TestTopologySourcesSpec(t *testing.T) {
	frames := hopFetchFrames()
	backend := &hopFetchBackend{t: t, data: defaultSchemaHopFixture(frames)}
	model := topologyRequestModel(backend)
	_, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), topologyRangeRequest(frames, 1))
	require.ErrorContains(t, err, "topology sources are empty")

	sources := make([]cmdb.TopologySource, MaxTopologySources+1)
	for i := range sources {
		sources[i] = podSource(fmt.Sprintf("pod-%d", i))
	}
	_, err = model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), topologyRangeRequest(frames, 1, sources...))
	var limitErr *ResultLimitError
	require.True(t, errors.As(err, &limitErr), "got %v", err)
	require.Equal(t, "max_topology_sources", limitErr.Reason)
	require.Equal(t, MaxTopologySources+1, limitErr.Count)
	require.Equal(t, 20, limitErr.Limit)
	require.Empty(t, backend.calls, "rejected before any backend read")

	_, err = model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), topologyRangeRequest(frames, 1, sources[:MaxTopologySources]...))
	require.NoError(t, err)

	missing := []cmdb.TopologySource{podSource("pod-1"), {SourceInfo: cmdb.Matcher{"pod": "pod-2"}}}
	_, err = model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), topologyRangeRequest(frames, 1, missing...))
	require.ErrorContains(t, err, "source type is empty in sources[1]")
}

// fetchSpans 记录 timegraph-select-fetch 的 fetch-mode 与 fetch-fallback 取值。
func fetchSpans(t *testing.T, run func()) (modes map[string]bool, fallbacks map[string]bool) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer func() { otel.SetTracerProvider(previous); require.NoError(t, provider.Shutdown(context.Background())) }()
	run()
	modes, fallbacks = map[string]bool{}, map[string]bool{}
	for _, span := range recorder.Ended() {
		if span.Name() != "timegraph-select-fetch" {
			continue
		}
		for _, kv := range span.Attributes() {
			switch kv.Key {
			case attribute.Key("fetch-mode"):
				modes[kv.Value.AsString()] = true
			case attribute.Key("fetch-fallback"):
				fallbacks[kv.Value.AsString()] = true
			}
		}
	}
	return modes, fallbacks
}

// 多个选择器时每帧的根取并集、跳数从最近的根起算。参照有两个：
// 节点等于各选择器单独查询（取数路径未变）每帧节点的并集；整个答案等于不按根收窄、整条读取候选关系的结果。
func TestMultiSourceTopologyMatchesUnionAndFullRead(t *testing.T) {
	oldPlan := SharedTopologyPlanCandidates
	t.Cleanup(func() { SharedTopologyPlanCandidates = oldPlan })
	frames := hopFetchFrames()
	data := defaultSchemaHopFixture(frames)
	container := cmdb.TopologySource{SourceType: "container", SourceInfo: cmdb.Matcher{"bcs_cluster_id": "cluster-a", "namespace": "ns-a", "pod": "pod-1", "container": "c0"}}
	replicaset := cmdb.TopologySource{SourceType: "replicaset", SourceInfo: cmdb.Matcher{"bcs_cluster_id": "cluster-a", "namespace": "ns-a", "replicaset": "rs-2"}}
	for _, tc := range []struct {
		name    string
		sources []cmdb.TopologySource
	}{
		{name: "same-type", sources: []cmdb.TopologySource{podSource("pod-1"), podSource("pod-4")}},
		{name: "mixed-types", sources: []cmdb.TopologySource{container, replicaset}},
	} {
		for _, hops := range []int{1, 2} {
			for _, direction := range []string{"both", "out"} {
				t.Run(fmt.Sprintf("%s/H%d/%s", tc.name, hops, direction), func(t *testing.T) {
					request := topologyRangeRequest(frames, hops, tc.sources...)
					request.Direction = direction
					SharedTopologyPlanCandidates = false
					_, wantCanonical, wantCompact := topologyRequestResult(t, &hopFetchBackend{t: t, data: data}, request)

					SharedTopologyPlanCandidates = true
					backend := &hopFetchBackend{t: t, data: data}
					var snapshots []cmdb.SharedTopologySnapshot
					var gotCanonical, gotCompact []string
					modes, fallbacks := fetchSpans(t, func() { snapshots, gotCanonical, gotCompact = topologyRequestResult(t, backend, request) })
					require.Equal(t, wantCanonical, gotCanonical)
					require.Equal(t, wantCompact, gotCompact)
					require.Equal(t, map[string]bool{"hop": true}, modes)
					require.Empty(t, fallbacks)
					for _, call := range backend.calls {
						require.True(t, hopFetchNarrowed(call.conditions), "%s must be read by root conditions or reached identities", call.metric)
					}

					union := make([]map[string]bool, len(frames))
					for i := range union {
						union[i] = map[string]bool{}
					}
					for _, source := range tc.sources {
						single := topologyRangeRequest(frames, hops, source)
						single.Direction = direction
						singleSnapshots, _, _ := topologyRequestResult(t, &hopFetchBackend{t: t, data: data}, single)
						for i, keys := range nodeKeysByFrame(singleSnapshots) {
							for key := range keys {
								union[i][key] = true
							}
						}
					}
					require.Equal(t, union, nodeKeysByFrame(snapshots))
				})
			}
		}
	}
}

// 同一关系、同一端上的多个根写成一个 OR 查询读取，不按根各读一次。
func TestMultiSourceRootReadsShareOneQuery(t *testing.T) {
	frames := hopFetchFrames()
	backend := &hopFetchBackend{t: t, data: defaultSchemaHopFixture(frames)}
	topologyRequestResult(t, backend, topologyRangeRequest(frames, 1, podSource("pod-1"), podSource("pod-4")))
	combined := 0
	for _, call := range backend.calls {
		values := map[string]bool{}
		for _, field := range call.conditions.FieldList {
			if field.DimensionName == "pod" {
				for _, value := range field.Value {
					values[value] = true
				}
			}
		}
		if values["pod-1"] && values["pod-4"] {
			combined++
			require.Contains(t, call.conditions.ConditionList, "or")
		}
	}
	require.Positive(t, combined, "root reads of the same relation side carry both selectors")
}

// 每个选择器的 source_expand_info 只筛选它自己的根：容器 pod-0/c0 的 info 指标首帧 version=v1、
// 后三帧 version=v2，replicaset rs-2 没有属性条件，四帧都是根。参照是两个选择器分别单独查询的每帧节点并集。
func TestMultiSourceExpandInfoAppliesPerSelector(t *testing.T) {
	frames := hopFetchFrames()
	data := defaultSchemaHopFixture(frames)
	identity := map[string]string{"bcs_cluster_id": "cluster-a", "namespace": "ns-a", "pod": "pod-0", "container": "c0"}
	info := func(version string) map[string]string {
		labels := map[string]string{"version": version}
		for key, value := range identity {
			labels[key] = value
		}
		return labels
	}
	data["container_info_relation"] = append(contractMatrix(info("v1"), frames[0]), contractMatrix(info("v2"), frames[1:]...)...)
	container := cmdb.TopologySource{
		SourceType:       "container",
		SourceInfo:       cmdb.Matcher{"bcs_cluster_id": "cluster-a", "namespace": "ns-a", "pod": "pod-0", "container": "c0"},
		SourceExpandInfo: cmdb.Matcher{"version": "v2"},
	}
	replicaset := cmdb.TopologySource{SourceType: "replicaset", SourceInfo: cmdb.Matcher{"bcs_cluster_id": "cluster-a", "namespace": "ns-a", "replicaset": "rs-2"}}
	request := topologyRangeRequest(frames, 0, container, replicaset)
	snapshots, _, _ := topologyRequestResult(t, &hopFetchBackend{t: t, data: data}, request)
	union := make([]map[string]bool, len(frames))
	for i := range union {
		union[i] = map[string]bool{}
	}
	singles := map[cmdb.Resource][]bool{}
	for _, source := range []cmdb.TopologySource{container, replicaset} {
		single, _, _ := topologyRequestResult(t, &hopFetchBackend{t: t, data: data}, topologyRangeRequest(frames, 0, source))
		present := make([]bool, len(frames))
		for i, keys := range nodeKeysByFrame(single) {
			present[i] = len(keys) > 0
			for key := range keys {
				union[i][key] = true
			}
		}
		singles[source.SourceType] = present
	}
	require.Equal(t, []bool{false, true, true, true}, singles["container"], "the container selector is a root only where version=v2")
	require.Equal(t, []bool{true, true, true, true}, singles["replicaset"])
	require.Equal(t, union, nodeKeysByFrame(snapshots))
}

// 任一选择器不满足逐跳取数资格（这里 node 选择器没有身份条件）时，整项退回为整条读取候选关系，
// 答案与不按根收窄的读取相同，并在取数 span 上记录有界原因。
func TestMultiSourceFallsBackWhenSelectorNotEligible(t *testing.T) {
	oldPlan := SharedTopologyPlanCandidates
	t.Cleanup(func() { SharedTopologyPlanCandidates = oldPlan })
	frames := hopFetchFrames()
	data := defaultSchemaHopFixture(frames)
	request := topologyRangeRequest(frames, 1, podSource("pod-1"), cmdb.TopologySource{SourceType: "node"})
	SharedTopologyPlanCandidates = false
	_, want, wantCompact := topologyRequestResult(t, &hopFetchBackend{t: t, data: data}, request)
	SharedTopologyPlanCandidates = true
	var got, gotCompact []string
	modes, fallbacks := fetchSpans(t, func() { _, got, gotCompact = topologyRequestResult(t, &hopFetchBackend{t: t, data: data}, request) })
	require.Equal(t, want, got)
	require.Equal(t, wantCompact, gotCompact)
	require.Equal(t, map[string]bool{"bulk": true}, modes)
	require.Equal(t, map[string]bool{"selector_not_eligible": true}, fallbacks)
}

// target_types 过滤时保留全部选择器的类型，即使它们不在 target_types 里。
func TestMultiSourceTargetTypesKeepAllSelectorTypes(t *testing.T) {
	frames := hopFetchFrames()
	data := defaultSchemaHopFixture(frames)
	container := cmdb.TopologySource{SourceType: "container", SourceInfo: cmdb.Matcher{"bcs_cluster_id": "cluster-a", "namespace": "ns-a", "pod": "pod-1", "container": "c0"}}
	replicaset := cmdb.TopologySource{SourceType: "replicaset", SourceInfo: cmdb.Matcher{"bcs_cluster_id": "cluster-a", "namespace": "ns-a", "replicaset": "rs-2"}}
	request := topologyRangeRequest(frames, 2, container, replicaset)
	request.TargetTypes = []cmdb.Resource{"node"}
	snapshots, canonical, compact := topologyRequestResult(t, &hopFetchBackend{t: t, data: data}, request)
	require.Equal(t, canonical, compact)
	require.Equal(t, map[cmdb.Resource]bool{"container": true, "replicaset": true, "node": true}, resourceTypesOf(snapshots))
}

// 多个选择器时节点集是各根 H 跳内节点的并集，边是这个节点集内的全部关联（诱导子图）。一条关系的两端
// 分别只在不同选择器的 H 跳范围内时也要输出：裁剪候选关系按全部选择器可达类型的并集，不按每个选择器
// 分别裁剪再取并集。期望按诱导子图的定义直接列出，逐跳与整条读取两种取数、快照与紧凑两种格式都要相同。
func TestMultiSourceKeepsEdgesBetweenSelectorNeighborhoods(t *testing.T) {
	oldPlan := SharedTopologyPlanCandidates
	t.Cleanup(func() { SharedTopologyPlanCandidates = oldPlan })
	SharedTopologyPlanCandidates = true
	type scenario struct {
		name    string
		model   func(*hopFetchBackend) *Model
		data    map[string]pl.Matrix
		request cmdb.SharedTopologyQuery
		wide    cmdb.TopologySource // 替换第二个选择器，使它不满足逐跳取数条件
		nodes   []cmdb.Resource
		edges   []string
	}
	var scenarios []scenario
	// 通用关系 a–x、b–y、x–y：以 a、b 为入口走 1 跳，a 只到 x、b 只到 y，x–y 的两端分属两个入口。
	for _, category := range []RelationCategory{RelationCategoryDynamic, RelationCategoryStatic} {
		provider := contractSchemaProvider{
			resources: []ResourceType{"a", "b", "x", "y"},
			primary:   map[ResourceType][]string{"a": {"a_id"}, "b": {"b_id"}, "x": {"x_id"}, "y": {"y_id"}},
			fields:    map[ResourceType][]string{"a": {"a_id"}, "b": {"b_id"}, "x": {"x_id"}, "y": {"y_id"}},
		}
		fixture := newHopFetchFixture()
		var edges []string
		for _, pair := range [][2]string{{"a", "x"}, {"b", "y"}, {"x", "y"}} {
			name := pair[0] + "_to_" + pair[1]
			provider.schemas = append(provider.schemas, RelationSchema{RelationType: RelationType(name), Category: category, FromType: ResourceType(pair[0]), ToType: ResourceType(pair[1]), MetricName: name, IsDirectional: category == RelationCategoryDynamic})
			from, to := pair[0]+"_id", pair[1]+"_id"
			if category == RelationCategoryDynamic {
				from, to = "from_"+from, "to_"+to
			}
			fixture.add(name, map[string]string{from: pair[0], to: pair[1]}, hopFetchStart)
			// 动态关系按 out 只有调用方 → 被调用方；不分方向的静态关系两个视图各一条。
			edges = append(edges, strings.Join([]string{name, pair[0], pair[1], string(DirectionOutbound)}, "|"))
			if category == RelationCategoryStatic {
				edges = append(edges, strings.Join([]string{name, pair[1], pair[0], string(DirectionInbound)}, "|"))
			}
		}
		request := cmdb.SharedTopologyQuery{SpaceUID: "space", Timestamp: hopFetchStart / 1000, MaxHops: 1, Direction: "both", Sources: []cmdb.TopologySource{
			{SourceType: "a", SourceInfo: cmdb.Matcher{"a_id": "a"}},
			{SourceType: "b", SourceInfo: cmdb.Matcher{"b_id": "b"}},
		}}
		if category == RelationCategoryDynamic {
			request.Direction = "out"
		}
		scenarios = append(scenarios, scenario{
			name:    string(category),
			model:   func(backend *hopFetchBackend) *Model { return hopFetchModel(provider, backend) },
			data:    fixture.data,
			request: request,
			wide:    cmdb.TopologySource{SourceType: "b"},
			nodes:   []cmdb.Resource{"a", "b", "x", "y"},
			edges:   edges,
		})
	}
	// 内置归属链 container → pod → replicaset → deployment：以 container、deployment 为入口走 1 跳，
	// pod 与 replicaset 分属两个入口，pod_with_replicaset 也在诱导子图里。
	fixture := newHopFetchFixture()
	workload := map[string]string{"bcs_cluster_id": "cluster", "namespace": "ns"}
	with := func(extra map[string]string) map[string]string {
		labels := maps.Clone(workload)
		maps.Copy(labels, extra)
		return labels
	}
	fixture.add("container_with_pod_relation", with(map[string]string{"pod": "pod-0", "container": "c0"}), hopFetchStart)
	fixture.add("pod_with_replicaset_relation", with(map[string]string{"pod": "pod-0", "replicaset": "rs-0"}), hopFetchStart)
	fixture.add("deployment_with_replicaset_relation", with(map[string]string{"deployment": "web", "replicaset": "rs-0"}), hopFetchStart)
	scenarios = append(scenarios, scenario{
		name:  "builtin-belong-to",
		model: topologyRequestModel,
		data:  fixture.data,
		request: cmdb.SharedTopologyQuery{SpaceUID: "space", Timestamp: hopFetchStart / 1000, MaxHops: 1, Direction: "both", RelationGroups: []string{RelationGroupBelongTo}, Sources: []cmdb.TopologySource{
			{SourceType: "container", SourceInfo: cmdb.Matcher(with(map[string]string{"pod": "pod-0", "container": "c0"}))},
			{SourceType: "deployment", SourceInfo: cmdb.Matcher(with(map[string]string{"deployment": "web"}))},
		}},
		wide:  cmdb.TopologySource{SourceType: "deployment"},
		nodes: []cmdb.Resource{"container", "deployment", "pod", "replicaset"},
		// 归属边每对节点一条，子 → 父。
		edges: []string{
			"container_with_pod|container|pod|outbound",
			"deployment_with_replicaset|replicaset|deployment|outbound",
			"pod_with_replicaset|pod|replicaset|outbound",
		},
	})
	describe := func(snapshot cmdb.SharedTopologySnapshot) ([]cmdb.Resource, []string) {
		types := map[uint64]cmdb.Resource{}
		var nodes []cmdb.Resource
		for _, node := range snapshot.Nodes {
			types[node.ID] = node.ResourceType
			nodes = append(nodes, node.ResourceType)
		}
		var edges []string
		for _, edge := range snapshot.Edges {
			edges = append(edges, strings.Join([]string{edge.RelationType, string(types[edge.Source]), string(types[edge.Target]), edge.Direction}, "|"))
		}
		slices.Sort(nodes)
		slices.Sort(edges)
		return nodes, edges
	}
	for _, tc := range scenarios {
		slices.Sort(tc.edges)
		for _, wide := range []bool{false, true} {
			request := tc.request
			request.Sources = slices.Clone(tc.request.Sources)
			mode := "hop"
			if wide {
				request.Sources[1] = tc.wide
				mode = "bulk"
			}
			for _, format := range []string{"", cmdb.CompactTopologyFormat} {
				t.Run(fmt.Sprintf("%s/%s/%q", tc.name, mode, format), func(t *testing.T) {
					request.ResponseFormat = format
					var result cmdb.SharedTopologyResult
					var err error
					modes, _ := fetchSpans(t, func() {
						result, err = tc.model(&hopFetchBackend{t: t, data: tc.data}).QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
					})
					require.NoError(t, err)
					require.Equal(t, map[string]bool{mode: true}, modes)
					snapshots := result.Snapshots
					if format != "" {
						snapshots = decodeCompactForTest(t, result.Compact)
					}
					require.Len(t, snapshots, 1)
					nodes, edges := describe(snapshots[0])
					require.Equal(t, tc.nodes, nodes)
					require.Equal(t, tc.edges, edges)
				})
			}
		}
	}
}

// 一条调用序列只输出一条边：方向为调用方 → 被调用方、direction 为 outbound，不论请求 direction 取哪个，
// 也不论遍历走的是 outbound 还是 inbound 视图。期望按调用序列与方向语义直接列出：
// a → b 在第 0、1 帧，c → a 在第 2 帧；out 只走 a 作为调用方的边，in 只走 a 作为被调用方的边。
func TestTopologyServiceCallOutputsOneEdgePerCall(t *testing.T) {
	provider := contractSchemaProvider{
		resources: []ResourceType{"svc"}, primary: map[ResourceType][]string{"svc": {"id"}}, fields: map[ResourceType][]string{"svc": {"id"}},
		schemas: []RelationSchema{{RelationType: "calls", Category: RelationCategoryDynamic, FromType: "svc", ToType: "svc", MetricName: "calls_flow", IsDirectional: true}},
	}
	responses := map[string]pl.Matrix{
		"calls_flow": append(contractMatrix(map[string]string{"from_id": "a", "to_id": "b"}, 1700000000000, 1700000060000),
			contractMatrix(map[string]string{"from_id": "c", "to_id": "a"}, 1700000120000)...),
	}
	ab, ca := "calls|a|b|outbound", "calls|c|a|outbound"
	for _, tc := range []struct {
		direction string
		frames    [][]string
	}{
		{direction: "both", frames: [][]string{{ab}, {ab}, {ca}}},
		{direction: "", frames: [][]string{{ab}, {ab}, {ca}}},
		{direction: "out", frames: [][]string{{ab}, {ab}, nil}},
		{direction: "in", frames: [][]string{nil, nil, {ca}}},
	} {
		for _, format := range []string{"", cmdb.CompactTopologyFormat} {
			t.Run(fmt.Sprintf("%s/%q", tc.direction, format), func(t *testing.T) {
				vm := &publicTimeGraphVM{responses: responses}
				model := &Model{schemaProvider: provider, timeGraphQueryReference: timeGraphTestQueryReference, timeGraphVMQuery: vm.query}
				result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{
					SpaceUID: "space", StartTime: 1700000000, EndTime: 1700000120, Step: "60s", LookBackDelta: "30s", MaxHops: 1,
					Direction: tc.direction, ResponseFormat: format, Sources: []cmdb.TopologySource{{SourceType: "svc", SourceInfo: cmdb.Matcher{"id": "a"}}},
				})
				require.NoError(t, err)
				snapshots := result.Snapshots
				if format != "" {
					snapshots = decodeCompactForTest(t, result.Compact)
				}
				require.Len(t, snapshots, len(tc.frames))
				for index, snapshot := range snapshots {
					ids := map[uint64]string{}
					for _, node := range snapshot.Nodes {
						ids[node.ID] = node.Dimensions["id"]
					}
					var edges []string
					for _, edge := range snapshot.Edges {
						edges = append(edges, strings.Join([]string{edge.RelationType, ids[edge.Source], ids[edge.Target], edge.Direction}, "|"))
					}
					require.Equal(t, tc.frames[index], edges, "frame %d", index)
				}
			})
		}
	}
}
