// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package onemodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type targetDirectoryFake struct {
	model      ModelDefinition
	spaces     map[int64]BusinessSpace
	group      DynamicGroupDefinition
	groupsRead int
	err        error
}

func (d *targetDirectoryFake) Model(context.Context, string, string) (ModelDefinition, bool, error) {
	return d.model, d.model.ModelID != "", d.err
}

func (d *targetDirectoryFake) Space(_ context.Context, _ string, id int64) (BusinessSpace, bool, error) {
	v, ok := d.spaces[id]
	return v, ok, d.err
}

func (d *targetDirectoryFake) DynamicGroup(context.Context, string, string) (DynamicGroupDefinition, bool, error) {
	d.groupsRead++
	return d.group, d.group.ID != "", d.err
}

type targetPagerFake struct {
	pages   []Page
	queries []PageQuery
	closed  []string
	failAt  int
	err     error
}

func (p *targetPagerFake) Search(ctx context.Context, _ string, q PageQuery) (Page, error) {
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	p.queries = append(p.queries, q)
	i := len(p.queries) - 1
	if p.failAt == i+1 {
		return Page{}, p.err
	}
	if i >= len(p.pages) {
		return Page{}, fmt.Errorf("unexpected query")
	}
	return p.pages[i], nil
}

func (p *targetPagerFake) Close(ctx context.Context, _ string, cursor string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.closed = append(p.closed, cursor)
	return nil
}

type targetTopologyFunc func(context.Context, string, int64, string, string) ([]InstanceRef, bool, error)

func (f targetTopologyFunc) Members(ctx context.Context, tenant string, biz int64, model, node string) ([]InstanceRef, bool, error) {
	return f(ctx, tenant, biz, model, node)
}

func targetFixtures() (*targetDirectoryFake, *targetPagerFake, TargetDescriptor) {
	d := &targetDirectoryFake{model: ModelDefinition{TenantID: "t", ModelID: "cw-Host", DataSource: "cmdb", CMDBObjectID: "host", AttributeTypes: map[string]InstanceAttributeType{"cpu": InstanceAttributeLong}}, spaces: map[int64]BusinessSpace{2: {TenantID: "t", BusinessID: 2}, 8: {TenantID: "t", BusinessID: 8}, 99: {TenantID: "t", BusinessID: 99, Global: true}}}
	p := &targetPagerFake{}
	desc := TargetDescriptor{SchemaVersion: 1, ModelID: "cw-Host", Selectors: []TargetSelector{{Type: "instances", Instances: []InstanceRef{{ModelID: "cw-Host", InstanceID: "a", EntityUID: "cw-Host|a"}}}}}
	return d, p, desc
}

func targetInstance(model, id string, biz int64) Instance {
	return Instance{TenantID: "t", ModelCode: model, InstanceID: id, Fields: map[string]any{"bk_biz_ids": []any{biz}}}
}

func targetRef(id string) InstanceRef {
	return InstanceRef{ModelID: "cw-Host", InstanceID: id, EntityUID: "cw-Host|" + id}
}

func TestTargetScopeUsesActualTenantGlobalBusiness(t *testing.T) {
	d, p, _ := targetFixtures()
	p.pages = []Page{{Instances: []Instance{targetInstance("cw-biz", "2", 2), targetInstance("cw-biz", "8", 8)}}}
	r := NewTargetResolver(d, p, nil)
	scope, err := r.ResolveScope(t.Context(), "t", "bkcc__99")
	if err != nil || !reflect.DeepEqual(scope.BusinessIDs, []int64{2, 8}) {
		t.Fatalf("scope %+v %v", scope, err)
	}
	if p.queries[0].ModelID != "cw-biz" || !p.queries[0].Where.Empty() {
		t.Fatal("global scope did not enumerate tenant businesses")
	}
	scope, err = r.ResolveScope(t.Context(), "t", "bkcc__2")
	if err != nil || !reflect.DeepEqual(scope.BusinessIDs, []int64{2}) || len(p.queries) != 1 {
		t.Fatalf("ordinary business broadened %+v %v", scope, err)
	}
	for _, code := range []string{"", "bkcc__0", "bkcc__-1", "bkcc__02", "bkcc__+2", "bkci__2", "2"} {
		if _, err := ParseBusinessSpace(code); err == nil {
			t.Fatalf("invalid scope %s", code)
		}
	}
	d.spaces[2] = BusinessSpace{TenantID: "other", BusinessID: 2}
	if _, err := r.ResolveScope(t.Context(), "t", "bkcc__2"); !errors.Is(err, ErrTargetUnavailable) {
		t.Fatal(err)
	}
}

func TestStaticTargetsDeduplicateAndRequireCompleteScope(t *testing.T) {
	d, p, desc := targetFixtures()
	desc.Selectors[0].Instances = append(desc.Selectors[0].Instances, targetRef("a"))
	p.pages = []Page{{Instances: []Instance{targetInstance("cw-Host", "a", 2)}}}
	result, err := NewTargetResolver(d, p, nil).Resolve(t.Context(), "t", "bkcc__2", desc)
	if err != nil || len(result.Instances) != 1 || result.Selectors[0].Matched != 1 {
		t.Fatalf("result %+v %v", result, err)
	}
	raw, _ := json.Marshal(p.queries[0].Where)
	if !strings.Contains(string(raw), `"field":"bk_biz_ids"`) || !strings.Contains(string(raw), `"field":"model_inst_id"`) {
		t.Fatalf("scope not pushed down %s", raw)
	}
	for _, instances := range [][]Instance{{}, {targetInstance("cw-Host", "a", 8)}, {targetInstance("cw-Host", "unexpected", 2)}, {targetInstance("wrong", "a", 2)}} {
		p.pages = []Page{{Instances: instances}}
		p.queries = nil
		result, err := NewTargetResolver(d, p, nil).Resolve(t.Context(), "t", "bkcc__2", desc)
		if err == nil || len(result.Instances) != 0 {
			t.Fatalf("partial/foreign target accepted %+v %v", result, err)
		}
	}
}

func TestTargetPaginationFailureReleasesSnapshotWithoutPartialResult(t *testing.T) {
	d, p, desc := targetFixtures()
	p.pages = []Page{{Instances: []Instance{targetInstance("cw-Host", "a", 2)}, NextCursor: "cursor-1"}}
	p.failAt = 2
	p.err = errors.New("query failed")
	result, err := NewTargetResolver(d, p, nil).Resolve(t.Context(), "t", "bkcc__2", desc)
	if !errors.Is(err, p.err) || len(result.Instances) != 0 || !reflect.DeepEqual(p.closed, []string{"cursor-1"}) {
		t.Fatalf("failed page %+v %v closes=%v", result, err, p.closed)
	}
	p.queries = nil
	p.closed = nil
	p.failAt = 0
	p.pages = append(p.pages, Page{Instances: []Instance{targetInstance("cw-Host", "a", 2)}, NextCursor: "cursor-1"})
	if _, err := NewTargetResolver(d, p, nil).Resolve(t.Context(), "t", "bkcc__2", desc); !errors.Is(err, ErrTargetUnavailable) {
		t.Fatal(err)
	}
}

func TestDynamicTargetsReadDefinitionsEachTimeAndRejectForeignGroup(t *testing.T) {
	d, p, desc := targetFixtures()
	desc.Selectors = []TargetSelector{{Type: "dynamic_group", Provider: "kingeye", DynamicGroupID: "23"}}
	d.group = DynamicGroupDefinition{TenantID: "t", ID: "23", ModelID: "cw-Host", SpaceCode: "bkcc__2", Conditions: json.RawMessage(`[{"field":"cpu","operator":"greater","value":"2"}]`)}
	p.pages = []Page{{Instances: []Instance{targetInstance("cw-Host", "a", 2)}}, {Instances: []Instance{targetInstance("cw-Host", "b", 2)}}}
	resolver := NewTargetResolver(d, p, nil)
	first, err := resolver.Resolve(t.Context(), "t", "bkcc__2", desc)
	if err != nil {
		t.Fatal(err)
	}
	d.group.Conditions = json.RawMessage(`[{"field":"cpu","operator":"greater","value":"5"}]`)
	next, err := resolver.Resolve(t.Context(), "t", "bkcc__2", desc)
	if err != nil || d.groupsRead != 2 || first.Instances[0].InstanceID == next.Instances[0].InstanceID {
		t.Fatalf("stale group result %+v %v", next, err)
	}
	a, _ := json.Marshal(p.queries[0].Where)
	b, _ := json.Marshal(p.queries[1].Where)
	if string(a) == string(b) || !strings.Contains(string(b), `"value":5`) {
		t.Fatalf("definition not evaluated %s", b)
	}
	for _, change := range []func(){func() { d.group.TenantID = "other" }, func() { d.group.TenantID = "t"; d.group.ModelID = "foreign" }, func() { d.group.ModelID = "cw-Host"; d.group.SpaceCode = "bkcc__8" }} {
		change()
		if _, err := resolver.Resolve(t.Context(), "t", "bkcc__2", desc); !errors.Is(err, ErrTargetUnavailable) {
			t.Fatal(err)
		}
	}
}

func TestTopologyRechecksEachBusinessBranch(t *testing.T) {
	d, p, desc := targetFixtures()
	desc.Selectors = []TargetSelector{{Type: "topo_node", Provider: "cmdb_mainline", TopologyNodeID: "module-1"}}
	p.pages = []Page{{Instances: []Instance{targetInstance("cw-biz", "2", 2), targetInstance("cw-biz", "8", 8)}}, {Instances: []Instance{targetInstance("cw-Host", "a", 8)}}}
	topo := targetTopologyFunc(func(_ context.Context, tenant string, biz int64, model, node string) ([]InstanceRef, bool, error) {
		if tenant != "t" || biz != 2 || model != "cw-Host" || node != "module-1" {
			t.Fatal("wrong topology request")
		}
		return []InstanceRef{targetRef("a")}, true, nil
	})
	if _, err := NewTargetResolver(d, p, topo).Resolve(t.Context(), "t", "bkcc__99", desc); !errors.Is(err, ErrTargetUnavailable) {
		t.Fatalf("foreign branch accepted %v", err)
	}
	p.queries = nil
	p.pages = []Page{{Instances: []Instance{targetInstance("cw-biz", "2", 2), targetInstance("cw-biz", "8", 8)}}, {Instances: []Instance{targetInstance("cw-Host", "b", 8)}}}
	biz := int64(8)
	desc.Selectors[0].BizID = &biz
	topo = targetTopologyFunc(func(_ context.Context, _ string, b int64, _, _ string) ([]InstanceRef, bool, error) {
		if b != 8 {
			t.Fatal("selector business ignored")
		}
		return []InstanceRef{targetRef("b")}, true, nil
	})
	result, err := NewTargetResolver(d, p, topo).Resolve(t.Context(), "t", "bkcc__99", desc)
	if err != nil || len(result.Instances) != 1 {
		t.Fatalf("topology %+v %v", result, err)
	}
}

func TestTargetCancellationAndUnavailableResources(t *testing.T) {
	d, p, desc := targetFixtures()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewTargetResolver(d, p, nil).Resolve(ctx, "t", "bkcc__2", desc); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := NewTargetResolver(nil, p, nil).Resolve(t.Context(), "t", "bkcc__2", desc); !errors.Is(err, ErrTargetUnavailable) {
		t.Fatal(err)
	}
	if _, err := NewTargetResolver(d, nil, nil).Resolve(t.Context(), "t", "bkcc__2", desc); !errors.Is(err, ErrTargetUnavailable) {
		t.Fatal(err)
	}
}

// 三类 selector 必须全部完整，顺序只影响逐项诊断，不影响最终并集。
func TestMixedTargetsUnionAndLateFailure(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(fmt.Sprintf("late_failure_%t", bad), func(t *testing.T) {
			d, p, desc := targetFixtures()
			d.group = DynamicGroupDefinition{TenantID: "t", ID: "7", ModelID: "cw-Host", SpaceCode: "bkcc__2", Conditions: json.RawMessage(`[{"field":"cpu","operator":"greater","value":0}]`)}
			biz := int64(2)
			desc.Selectors = []TargetSelector{
				{Type: "instances", Instances: []InstanceRef{targetRef("a"), targetRef("b"), targetRef("a")}},
				{Type: "topo_node", Provider: "cmdb_mainline", TopologyNodeID: "module-1", BizID: &biz},
				{Type: "dynamic_group", Provider: "kingeye", DynamicGroupID: "7"},
			}
			p.pages = []Page{
				{Instances: []Instance{targetInstance("cw-Host", "a", 2), targetInstance("cw-Host", "b", 2)}},
				{Instances: []Instance{targetInstance("cw-Host", "b", 2), targetInstance("cw-Host", "c", 2)}},
				{Instances: []Instance{targetInstance("cw-Host", "c", 2), targetInstance("cw-Host", "d", 2)}},
			}
			if bad {
				p.failAt = 3
				p.err = errors.New("last selector unavailable")
			}
			topo := targetTopologyFunc(func(context.Context, string, int64, string, string) ([]InstanceRef, bool, error) {
				return []InstanceRef{targetRef("b"), targetRef("c"), targetRef("b")}, true, nil
			})
			result, err := NewTargetResolver(d, p, topo).Resolve(t.Context(), "t", "bkcc__2", desc)
			if bad {
				if !errors.Is(err, p.err) || len(result.Instances) != 0 || len(result.Selectors) != 0 {
					t.Fatalf("partial selectors leaked: %+v %v", result, err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(result.Instances, []InstanceRef{targetRef("a"), targetRef("b"), targetRef("c"), targetRef("d")}) {
				t.Fatalf("union=%+v error=%v", result, err)
			}
			if !reflect.DeepEqual(result.Selectors, []TargetSelectorResult{{Index: 0, Type: "instances", Matched: 2}, {Index: 1, Type: "topo_node", Matched: 2}, {Index: 2, Type: "dynamic_group", Matched: 2}}) {
				t.Fatalf("selector counts confused with union: %+v", result.Selectors)
			}
		})
	}
}

func TestRepeatedDynamicSelectorsShareCandidateBudget(t *testing.T) {
	for _, count := range []int{2, 3} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			d, p, desc := targetFixtures()
			d.group = DynamicGroupDefinition{TenantID: "t", ID: "7", ModelID: "cw-Host", SpaceCode: "bkcc__2", Conditions: json.RawMessage(`[{"field":"cpu","operator":"greater","value":0}]`)}
			desc.Selectors = nil
			for selector := 0; selector < count; selector++ {
				desc.Selectors = append(desc.Selectors, TargetSelector{Type: "dynamic_group", Provider: "kingeye", DynamicGroupID: "7"})
				for pageIndex := 0; pageIndex < 50; pageIndex++ {
					page := Page{}
					for row := 0; row < 200; row++ {
						page.Instances = append(page.Instances, targetInstance("cw-Host", fmt.Sprintf("host-%05d", pageIndex*200+row), 2))
					}
					if pageIndex < 49 {
						page.NextCursor = fmt.Sprintf("s%d-page-%d", selector, pageIndex)
					}
					p.pages = append(p.pages, page)
				}
			}
			result, err := NewTargetResolver(d, p, nil).Resolve(t.Context(), "t", "bkcc__2", desc)
			if count == 2 {
				if err != nil || len(result.Instances) != 10000 || len(result.Selectors) != 2 {
					t.Fatalf("exact 20000 candidate budget rejected: members=%d selectors=%d error=%v", len(result.Instances), len(result.Selectors), err)
				}
			} else if !errors.Is(err, ErrResultLimit) || len(result.Instances) != 0 || len(p.queries) != 101 || !reflect.DeepEqual(p.closed, []string{"s2-page-0"}) {
				t.Fatalf("union dedup bypassed request budget: members=%d reads=%d closes=%v error=%v", len(result.Instances), len(p.queries), p.closed, err)
			}
		})
	}
}
