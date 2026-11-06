package prometheus

import (
	"context"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/elasticsearch"
)

func ClearScroll(ctx context.Context, target redisUtil.ElasticsearchScrollTarget, scrollIDs []string) error {
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
