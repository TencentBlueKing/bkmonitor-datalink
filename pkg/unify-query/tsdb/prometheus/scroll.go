package prometheus

import (
	"context"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/elasticsearch"
)

// ClearScroll 按会话保存的目标恢复 ES 实例，复用查询侧的存储配置、BkData 网关路由及鉴权。
func ClearScroll(ctx context.Context, target redisUtil.ElasticsearchScrollTarget, scrollIDs []string) error {
	// 每个目标使用独立的用户副本和 metadata 标识，避免并发清理串用空间信息，
	// 也避免构建实例时记录的错误状态覆盖原查询状态。
	user := *metadata.GetUser(ctx)
	user.SpaceUID, user.TenantID = target.SpaceUID, target.TenantID
	ctx = metadata.InitHashID(ctx)
	metadata.SetUser(ctx, &user)
	instance, ok := GetTsDbInstance(ctx, &metadata.Query{
		StorageID: target.StorageID, SourceType: target.SourceType,
		StorageType: metadata.ElasticsearchStorageType,
	}).(*elasticsearch.Instance)
	if !ok {
		return errors.New("scroll cleanup instance unavailable")
	}
	return instance.ClearScroll(ctx, scrollIDs...)
}
