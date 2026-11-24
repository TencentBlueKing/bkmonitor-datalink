package http

import (
	"context"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

// 保持已发布版本的会话键，升级和回滚后仍能读取同一次下载的最新游标。
// query 是 JSON 对象，会话与锁的首个 Redis hash tag 相同。
func scrollSessionKey(user *metadata.User, query string) string {
	return fmt.Sprintf("%s:%s", user.Name, query)
}

// 普通路由保留旧版分支键；只有同表、同 StorageID 同时访问两种 ES 来源时才区分游标。
func scrollSliceKey(query *metadata.Query, nativeESRoutes map[string]bool) string {
	key := query.TableUUID()
	route := *query
	route.SliceID = ""
	if query.StorageType == metadata.ElasticsearchStorageType && query.SourceType == structured.BkData && nativeESRoutes[route.TableUUID()] {
		key += "|bkdata"
	}
	return key
}

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
