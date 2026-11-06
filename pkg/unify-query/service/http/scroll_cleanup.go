package http

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

// setScrollTarget 首次遇到 ES 分片时保存清理路由，随后随会话一起持久化。
// 已保存的目标保持不变，确保回收仍使用该游标所属的存储和空间。
func setScrollTarget(ctx context.Context, slice *redisUtil.SliceStatus, query *metadata.Query) {
	if query.StorageType != metadata.ElasticsearchStorageType || slice.ScrollTarget != nil {
		return
	}
	user := metadata.GetUser(ctx)
	slice.ScrollTarget = &redisUtil.ElasticsearchScrollTarget{
		StorageID: query.StorageID, SourceType: query.SourceType,
		SpaceUID: user.SpaceUID, TenantID: user.TenantID,
	}
}
