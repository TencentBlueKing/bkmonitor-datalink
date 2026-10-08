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
	"slices"
	"strconv"
	"strings"
	"time"
)

// ErrTargetUnavailable 表示目标集合不能完整解析，消费者应跳过该策略而不是放宽过滤。
var ErrTargetUnavailable = errors.New("target resolution unavailable")

// ModelDefinition 是当前租户对象目录的最小执行期投影。
type ModelDefinition struct {
	TenantID       string
	ModelID        string
	DataSource     string
	CMDBObjectID   string
	AttributeTypes map[string]InstanceAttributeType
}

// BusinessSpace 保存 Kingeye 业务空间的真实全局标记；0 不是全局业务的别名。
type BusinessSpace struct {
	TenantID   string
	BusinessID int64
	Global     bool
}

// DynamicGroupDefinition 保存实时定义；故意不包含成员缓存、代次或展开快照。
type DynamicGroupDefinition struct {
	TenantID   string
	ID         string
	ModelID    string
	SpaceCode  string
	Conditions json.RawMessage
}

// TargetDirectory 在当前租户读取定义，返回 found=false 必须与读取失败分开。
// 实现不得回退到其他租户或 dynamic_group_member_v2 成员快照。
type TargetDirectory interface {
	Model(context.Context, string, string) (ModelDefinition, bool, error)
	Space(context.Context, string, int64) (BusinessSpace, bool, error)
	DynamicGroup(context.Context, string, string) (DynamicGroupDefinition, bool, error)
}

// TargetPager 提供实例完整分页和取消时释放快照；生产使用 OneModel Pager。
type TargetPager interface {
	Search(context.Context, string, PageQuery) (Page, error)
	Close(context.Context, string, string) error
}

// TargetTopology 读取当前业务的节点及完整成员，并检查 canonical 身份。
// 未发现节点与完整空成员必须区分，不能把 backend 部分失败变成空集合。
type TargetTopology interface {
	Members(context.Context, string, int64, string, string) ([]InstanceRef, bool, error)
}

// LiveTargetReader 读取 CMDB 权威事实；显式服务实例不能从主机投影或空 ES 查询代替。
// TopologyInstances 必须先校验节点的租户模型与业务归属，found=false 与读取失败分开返回。
type LiveTargetReader interface {
	ServiceInstances(context.Context, string, int64, string, []string) ([]Instance, error)
	TopologyInstances(context.Context, string, int64, ModelDefinition, string) ([]Instance, bool, error)
}

// TargetResolverOption 在构造时装配可选读取端口，不改变目录及业务范围的权威来源。
type TargetResolverOption func(*TargetResolver)

// WithLiveTargetReader 注入部署级 CMDB 实时读取，缺少该资源时需要回源的策略返回不可求值。
func WithLiveTargetReader(reader LiveTargetReader) TargetResolverOption {
	return func(r *TargetResolver) { r.live = reader }
}

// TargetScope 是从真实业务目录解析的授权业务集合，不接受调用方伪造 global 标记。
type TargetScope struct {
	TenantID    string
	SpaceCode   string
	BusinessIDs []int64
}

// TargetResult 只在全部 selector 成功时返回完整排序集合。
type TargetResult struct {
	Scope     TargetScope
	Instances []InstanceRef
	Selectors []TargetSelectorResult
}

// TargetSelectorResult 保存可观测计数，不包含敏感条件值。
type TargetSelectorResult struct {
	Index   int    `json:"index"`
	Type    string `json:"type"`
	Matched int    `json:"matched"`
}

// TargetResolver 每次解析重新读取定义，缓存仅限调用者明确的一次 Event/预览上下文。
// 一个调用最多读取 256 页、20000 个候选、10000 个最终实例，避免跨业务/selector 放大无界。
type TargetResolver struct {
	directory TargetDirectory
	pager     TargetPager
	topology  TargetTopology
	live      LiveTargetReader
}

// NewTargetResolver 注入只读资源；未配置依赖会在实际使用时明确失败，不产生空成功。
func NewTargetResolver(directory TargetDirectory, pager TargetPager, topology TargetTopology, options ...TargetResolverOption) *TargetResolver {
	r := &TargetResolver{directory: directory, pager: pager, topology: topology}
	for _, option := range options {
		option(r)
	}
	return r
}

// ParseBusinessSpace 仅接受 canonical bkcc__<positive-id>，全局属性仍须读目录。
func ParseBusinessSpace(code string) (int64, error) {
	if !strings.HasPrefix(code, "bkcc__") {
		return 0, fmt.Errorf("%w: business space must use bkcc__<id>", ErrInvalidQuery)
	}
	text := strings.TrimPrefix(code, "bkcc__")
	id, err := strconv.ParseInt(text, 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != text {
		return 0, fmt.Errorf("%w: invalid business space", ErrInvalidQuery)
	}
	return id, nil
}

func (r *TargetResolver) space(ctx context.Context, tenant string, id int64) (BusinessSpace, error) {
	if r.directory == nil {
		return BusinessSpace{}, ErrTargetUnavailable
	}
	space, found, err := r.directory.Space(ctx, tenant, id)
	if err != nil {
		return BusinessSpace{}, err
	}
	if !found || space.TenantID != tenant || space.BusinessID != id {
		return BusinessSpace{}, fmt.Errorf("%w: space not found or identity mismatch", ErrTargetUnavailable)
	}
	return space, nil
}

type targetBudget struct{ pages, rows, bytes int }

// ResolveScope 将全局空间展开为当前租户真实可用业务，普通业务仍只返回自身。
func (r *TargetResolver) ResolveScope(ctx context.Context, tenant, spaceCode string) (TargetScope, error) {
	return r.resolveScope(ctx, tenant, spaceCode, &targetBudget{})
}

func (r *TargetResolver) resolveScope(ctx context.Context, tenant, spaceCode string, budget *targetBudget) (TargetScope, error) {
	scope := TargetScope{TenantID: tenant, SpaceCode: spaceCode, BusinessIDs: []int64{}}
	if ctx == nil {
		return scope, fmt.Errorf("%w: context is required", ErrInvalidQuery)
	}
	if err := validateOneModelIdentity("tenant", tenant, 64); err != nil {
		return scope, err
	}
	if err := ctx.Err(); err != nil {
		return scope, err
	}
	id, err := ParseBusinessSpace(spaceCode)
	if err != nil {
		return scope, err
	}
	space, err := r.space(ctx, tenant, id)
	if err != nil {
		return scope, err
	}
	if !space.Global {
		scope.BusinessIDs = []int64{id}
		return scope, nil
	}
	businesses, err := r.all(ctx, tenant, bizModelCode, Filter{}, budget)
	if err != nil {
		return scope, err
	}
	seen := map[int64]bool{}
	for _, business := range businesses {
		biz, ok := positiveInt64(business.InstanceID)
		if !ok {
			return scope, fmt.Errorf("%w: invalid business identity", ErrTargetUnavailable)
		}
		if biz != id {
			seen[biz] = true
		}
	}
	for biz := range seen {
		scope.BusinessIDs = append(scope.BusinessIDs, biz)
	}
	slices.Sort(scope.BusinessIDs)
	return scope, nil
}

// Resolve 不返回部分目标：静态实例缺失、动态组越界、分页超限或任一 selector 失败都会拒绝本次集合。
func (r *TargetResolver) Resolve(ctx context.Context, tenant, spaceCode string, descriptor TargetDescriptor) (TargetResult, error) {
	if ctx == nil {
		return TargetResult{}, fmt.Errorf("%w: context is required", ErrInvalidQuery)
	}
	if err := validateOneModelIdentity("tenant", tenant, 64); err != nil {
		return TargetResult{}, err
	}
	if err := descriptor.Validate(); err != nil {
		return TargetResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return TargetResult{}, err
	}
	if r.directory == nil {
		return TargetResult{}, ErrTargetUnavailable
	}
	model, found, err := r.directory.Model(ctx, tenant, descriptor.ModelID)
	if err != nil {
		return TargetResult{}, err
	}
	if !found || model.TenantID != tenant || model.ModelID != descriptor.ModelID {
		return TargetResult{}, fmt.Errorf("%w: model not found or identity mismatch", ErrTargetUnavailable)
	}
	budget := &targetBudget{}
	scope, err := r.resolveScope(ctx, tenant, spaceCode, budget)
	if err != nil {
		return TargetResult{}, err
	}
	result := TargetResult{Scope: scope, Instances: []InstanceRef{}, Selectors: []TargetSelectorResult{}}
	members := map[string]InstanceRef{}
	for i, selector := range descriptor.Selectors {
		var instances []Instance
		if len(scope.BusinessIDs) > 0 {
			switch selector.Type {
			case "instances":
				instances, err = r.explicit(ctx, scope, model, selector.Instances, budget)
			case "dynamic_group":
				instances, err = r.dynamic(ctx, scope, model, selector.DynamicGroupID, budget)
			case "topo_node":
				instances, err = r.topo(ctx, scope, model, selector, budget)
			}
		}
		if err != nil {
			return TargetResult{}, fmt.Errorf("target selector[%d]: %w", i, err)
		}
		result.Selectors = append(result.Selectors, TargetSelectorResult{Index: i, Type: selector.Type, Matched: len(instances)})
		for _, instance := range instances {
			ref := InstanceRef{ModelID: instance.ModelCode, InstanceID: instance.InstanceID, EntityUID: instance.ModelCode + "|" + instance.InstanceID}
			members[ref.EntityUID] = ref
			if len(members) > 10000 {
				return TargetResult{}, fmt.Errorf("%w: target set exceeds 10000", ErrResultLimit)
			}
		}
	}
	for _, ref := range members {
		result.Instances = append(result.Instances, ref)
	}
	slices.SortFunc(result.Instances, func(a, b InstanceRef) int { return strings.Compare(a.EntityUID, b.EntityUID) })
	return result, nil
}

func businessFilter(businesses []int64) Filter {
	// 单个 terms 最多 1024，较大的已授权集合拆成 OR；调用方已经验证非空和总预算。
	if len(businesses) <= 1024 {
		return Filter{Field: "bk_biz_ids", Type: InstanceAttributeLong, Operator: "in", Value: slices.Clone(businesses)}
	}
	f := Filter{}
	for start := 0; start < len(businesses); start += 1024 {
		f.Any = append(f.Any, businessFilter(businesses[start:min(start+1024, len(businesses))]))
	}
	return f
}

func scopedFilter(scope TargetScope, where Filter) Filter {
	business := businessFilter(scope.BusinessIDs)
	if where.Empty() {
		return business
	}
	return Filter{All: []Filter{business, where}}
}

func inBusinessScope(instance Instance, scope TargetScope) (bool, error) {
	raw, exists := instance.Fields["bk_biz_ids"]
	if !exists || raw == nil {
		return false, nil
	}
	var values []any
	switch x := raw.(type) {
	case []any:
		values = x
	case []int64:
		for _, id := range x {
			values = append(values, id)
		}
	default:
		return false, fmt.Errorf("%w: invalid instance businesses", ErrTargetUnavailable)
	}
	found := false
	for _, v := range values {
		id, ok := positiveInt64(v)
		if !ok {
			return false, fmt.Errorf("%w: invalid instance business", ErrTargetUnavailable)
		}
		if slices.Contains(scope.BusinessIDs, id) {
			found = true
		}
	}
	return found, nil
}

func (r *TargetResolver) all(ctx context.Context, tenant, model string, where Filter, budget *targetBudget) (out []Instance, err error) {
	if r.pager == nil {
		return nil, ErrTargetUnavailable
	}
	cursor := ""
	defer func() {
		if cursor != "" {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			err = errors.Join(err, r.pager.Close(cleanup, tenant, cursor))
		}
	}()
	seen := map[string]bool{}
	out = []Instance{}
	for {
		budget.pages++
		if budget.pages > 256 {
			return nil, fmt.Errorf("%w: target page budget exceeded", ErrResultLimit)
		}
		page, queryErr := r.pager.Search(ctx, tenant, PageQuery{ModelID: model, Where: where, Limit: 200, Cursor: cursor})
		if queryErr != nil {
			return nil, queryErr
		}
		previous := cursor
		cursor = page.NextCursor
		if len(page.Instances) > 200 || (cursor != "" && (cursor == previous || len(page.Instances) == 0)) {
			return nil, fmt.Errorf("%w: invalid target page", ErrTargetUnavailable)
		}
		for _, instance := range page.Instances {
			if instance.TenantID != tenant || instance.ModelCode != model || !selectionText(instance.InstanceID, 1024) || seen[instance.InstanceID] {
				return nil, fmt.Errorf("%w: duplicate or invalid instance identity", ErrTargetUnavailable)
			}
			encoded, encodeErr := json.Marshal(instance)
			if encodeErr != nil {
				return nil, ErrInvalidDataSourceResponse
			}
			budget.bytes += len(encoded)
			if budget.bytes > 32<<20 {
				return nil, fmt.Errorf("%w: target data exceeds 32 MiB", ErrResultLimit)
			}
			seen[instance.InstanceID] = true
			out = append(out, instance)
			budget.rows++
			if budget.rows > 20000 || len(out) > 10000 {
				return nil, fmt.Errorf("%w: target candidate budget exceeded", ErrResultLimit)
			}
		}
		if cursor == "" {
			return out, nil
		}
	}
}

func (r *TargetResolver) explicit(ctx context.Context, scope TargetScope, definition ModelDefinition, refs []InstanceRef, budget *targetBudget) ([]Instance, error) {
	model := definition.ModelID
	wanted := map[string]bool{}
	for _, ref := range refs {
		if ref.ModelID != model || ref.EntityUID != model+"|"+ref.InstanceID {
			return nil, ErrTargetUnavailable
		}
		wanted[ref.InstanceID] = true
	}
	ids := make([]string, 0, len(wanted))
	for id := range wanted {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	if isServiceModel(definition) {
		biz, err := ParseBusinessSpace(scope.SpaceCode)
		if err != nil || len(scope.BusinessIDs) != 1 || scope.BusinessIDs[0] != biz || r.live == nil {
			return nil, fmt.Errorf("%w: service instances require concrete business and live CMDB", ErrTargetUnavailable)
		}
		rows, err := r.live.ServiceInstances(ctx, scope.TenantID, biz, model, ids)
		if err != nil {
			return nil, err
		}
		if err := validateLiveRows(rows, scope, model, budget); err != nil {
			return nil, err
		}
		if len(rows) != len(ids) {
			return nil, ErrTargetUnavailable
		}
		for _, row := range rows {
			if !wanted[row.InstanceID] {
				return nil, ErrTargetUnavailable
			}
		}
		return rows, nil
	}
	out := []Instance{}
	for start := 0; start < len(ids); start += 512 {
		part := ids[start:min(start+512, len(ids))]
		filter := Filter{Field: "model_inst_id", Type: InstanceAttributeKeyword, Operator: "in", Value: part}
		rows, err := r.all(ctx, scope.TenantID, model, scopedFilter(scope, filter), budget)
		if err != nil {
			return nil, err
		}
		for _, instance := range rows {
			inScope, err := inBusinessScope(instance, scope)
			if err != nil {
				return nil, err
			}
			if !inScope || !slices.Contains(part, instance.InstanceID) {
				return nil, fmt.Errorf("%w: selected instance scope mismatch", ErrTargetUnavailable)
			}
			out = append(out, instance)
		}
	}
	if len(out) != len(wanted) {
		return nil, fmt.Errorf("%w: selected instance missing or outside business scope", ErrTargetUnavailable)
	}
	return out, nil
}

func (r *TargetResolver) dynamic(ctx context.Context, scope TargetScope, model ModelDefinition, id string, budget *targetBudget) ([]Instance, error) {
	group, found, err := r.directory.DynamicGroup(ctx, scope.TenantID, id)
	if err != nil {
		return nil, err
	}
	if !found || group.ID != id || group.TenantID != scope.TenantID || group.ModelID != model.ModelID {
		return nil, fmt.Errorf("%w: dynamic group scope/model mismatch", ErrTargetUnavailable)
	}
	if (model.DataSource != "cmdb" && model.DataSource != "legacy") || model.CMDBObjectID == "" {
		return nil, fmt.Errorf("%w: dynamic group model family unsupported", ErrTargetUnavailable)
	}
	if group.SpaceCode != "" {
		biz, err := ParseBusinessSpace(group.SpaceCode)
		if err != nil {
			return nil, err
		}
		space, err := r.space(ctx, scope.TenantID, biz)
		if err != nil {
			return nil, err
		}
		if !space.Global {
			if !slices.Contains(scope.BusinessIDs, biz) {
				return nil, fmt.Errorf("%w: dynamic group outside business scope", ErrTargetUnavailable)
			}
			scope.BusinessIDs = []int64{biz}
		}
	}
	filter, err := CompileDynamicConditions(group.Conditions, model)
	if err != nil {
		return nil, err
	}
	rows, err := r.all(ctx, scope.TenantID, model.ModelID, scopedFilter(scope, filter), budget)
	if err != nil {
		return nil, err
	}
	for _, instance := range rows {
		inScope, err := inBusinessScope(instance, scope)
		if err != nil {
			return nil, err
		}
		if !inScope {
			return nil, fmt.Errorf("%w: dynamic group member outside business scope", ErrTargetUnavailable)
		}
	}
	return rows, nil
}

func (r *TargetResolver) topo(ctx context.Context, scope TargetScope, model ModelDefinition, selector TargetSelector, budget *targetBudget) ([]Instance, error) {
	if r.topology == nil && r.live == nil {
		return nil, ErrTargetUnavailable
	}
	members := map[string]Instance{}
	found := false
	for _, biz := range scope.BusinessIDs {
		if selector.BizID != nil && *selector.BizID != biz {
			continue
		}
		budget.pages++
		if budget.pages > 256 {
			return nil, ErrResultLimit
		}
		branch := TargetScope{TenantID: scope.TenantID, SpaceCode: "bkcc__" + strconv.FormatInt(biz, 10), BusinessIDs: []int64{biz}}
		instances, present, err := r.topologyBranch(ctx, branch, model, selector.TopologyNodeID, budget)
		if err != nil {
			return nil, err
		}
		found = found || present
		if !present {
			if len(instances) > 0 {
				return nil, ErrTargetUnavailable
			}
			continue
		}
		// 同一个 locator 可在多个业务出现；每一支先按自己的业务校验，不能用全局业务并集掩盖越界成员。
		for _, instance := range instances {
			members[instance.InstanceID] = instance
			if len(members) > 10000 {
				return nil, ErrResultLimit
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("%w: topology node missing or outside business scope", ErrTargetUnavailable)
	}
	result := make([]Instance, 0, len(members))
	for _, member := range members {
		result = append(result, member)
	}
	slices.SortFunc(result, func(a, b Instance) int { return strings.Compare(a.InstanceID, b.InstanceID) })
	return result, nil
}
