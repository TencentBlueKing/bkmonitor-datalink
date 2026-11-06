package http

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

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
