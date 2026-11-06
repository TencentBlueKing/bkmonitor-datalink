package http

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

func TestSetScrollTarget(testContext *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2", TenantID: "tenant"})
	slice := &redisUtil.SliceStatus{}
	query := &metadata.Query{StorageType: metadata.InfluxDBStorageType, StorageID: "not-es"}
	setScrollTarget(ctx, slice, query)
	require.Nil(testContext, slice.ScrollTarget)

	query.StorageType, query.StorageID, query.SourceType = metadata.ElasticsearchStorageType, "es-cluster", structured.BkData
	setScrollTarget(ctx, slice, query)
	require.Equal(testContext, &redisUtil.ElasticsearchScrollTarget{
		StorageID: "es-cluster", SourceType: structured.BkData, SpaceUID: "bkcc__2", TenantID: "tenant",
	}, slice.ScrollTarget)

	query.StorageID = "other-cluster"
	setScrollTarget(ctx, slice, query)
	require.Equal(testContext, "es-cluster", slice.ScrollTarget.StorageID)
}
