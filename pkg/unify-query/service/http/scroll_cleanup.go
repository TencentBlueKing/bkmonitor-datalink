package http

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

// scrollSessionKey 将用户、租户与空间共同纳入会话身份，摘要避免在 Redis 键中保存查询正文。
func scrollSessionKey(user *metadata.User, query string) string {
	identity, _ := json.Marshal([]string{user.Name, user.TenantID, user.SpaceUID, query})
	// 会话与锁使用同一个 Redis Cluster hash tag，供原子保存同时访问两个键。
	return fmt.Sprintf("{%x}", sha256.Sum256(identity))
}

// BkData 使用独立网关；即使表和 StorageID 相同，也不能复用原生 ES 的游标。
// 响应中的 TableUUID 保持原格式，这个标识只用于会话内部保存分支。
func scrollSliceKey(query *metadata.Query) string {
	key := query.TableUUID()
	if query.StorageType == metadata.ElasticsearchStorageType && query.SourceType == structured.BkData {
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
