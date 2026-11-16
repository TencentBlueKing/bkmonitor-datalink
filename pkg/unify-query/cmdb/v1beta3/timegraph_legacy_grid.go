package v1beta3

import (
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// legacyRangeUsesVM 按原始请求范围判断旧候选路径是否使用 VM 缓存网格。
// 旧查询在合并表达式的任意路由包含 VM 时选择直查；混合路径共用该网格。
// 这里仅准备路由，实际取数仍按最终网格重新解析，保留存储切换时间边界。
func (m *Model) legacyRangeUsesVM(ctx context.Context, req *QueryRequest, path resourcePath, start, end time.Time, step, window time.Duration) (bool, error) {
	graph := newTimeGraphWithConfig(m.timeGraphConfig(req.SchemaNamespace()))
	source, target := cmdb.Resource(req.SourceType), cmdb.Resource(req.TargetType)
	rootOnly := len(path.Steps) <= 1
	if rootOnly && (!req.TargetInfoShow || len(graph.nodeBuilder.resourceInfoFields(source)) == 0) {
		return false, ctx.Err()
	}

	usesVM := func(query *structured.QueryTs, buildErr error) bool {
		if ctx.Err() != nil || buildErr != nil || query == nil {
			return false
		}
		queryCtx := newTimeGraphSubqueryContext(ctx)
		metadata.GetQueryParams(queryCtx).SetIsSkipK8s(true)
		query.NotTimeAlign = true
		var err error
		if m.timeGraphQueryReference != nil {
			_, err = m.timeGraphQueryReference(queryCtx, query)
		} else {
			_, err = query.ToQueryReference(queryCtx)
		}
		// 首跳为空时后续边不会执行，可选 Info 错误也有自己的质量处理。
		// 准备失败交由原取数阶段处理，不在这里增加失败或 partial 出口。
		return err == nil && metadata.GetQueryParams(queryCtx).IsDirectQuery()
	}

	relationPath := resourcePathsToTimeGraphRelationPaths([]resourcePath{path})[0]
	for hop := 1; hop < len(relationPath.Steps); hop++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		from, to := relationPath.Steps[hop-1].ResourceType, relationPath.Steps[hop].ResourceType
		for _, relation := range m.timeGraphRelationCandidates(req.SchemaNamespace(), from, to, relationPath.Steps[hop]) {
			var sourceInfo cmdb.Matcher
			if hop == 1 {
				sourceInfo = cmdb.Matcher(req.SourceInfo)
			}
			if usesVM(graph.MakeQueryTsWithWindow(ctx, req.SpaceUID, sourceInfo, start, end, step, window, relation)) {
				return true, ctx.Err()
			}
		}
	}
	if rootOnly || len(req.SourceExpandInfo) > 0 {
		if usesVM(graph.MakeResourceInfoQueryTsWithWindow(req.SpaceUID, source, req.SourceInfo, req.SourceExpandInfo, nil, start, end, step, window)) {
			return true, ctx.Err()
		}
	}
	// 旧表达式仅投影最终目标的 Info，中间节点的额外 Info 不决定返回网格。
	if !rootOnly && req.TargetInfoShow && len(graph.nodeBuilder.resourceInfoFields(target)) > 0 {
		if usesVM(graph.MakeResourceInfoQueryTsWithWindow(req.SpaceUID, target, nil, nil, nil, start, end, step, window)) {
			return true, ctx.Err()
		}
	}
	return false, ctx.Err()
}
