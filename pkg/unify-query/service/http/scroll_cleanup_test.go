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

func TestScrollSessionKey(testContext *testing.T) {
	user := metadata.User{Name: "reader", TenantID: "tenant-a", SpaceUID: "space-a"}
	key := scrollSessionKey(&user, "query")
	for _, testCase := range []struct {
		name  string
		user  metadata.User
		query string
		same  bool
	}{
		{name: "same_identity", user: user, query: "query", same: true},
		{name: "different_tenant", user: metadata.User{Name: user.Name, TenantID: "tenant-b", SpaceUID: user.SpaceUID}, query: "query"},
		{name: "different_space", user: metadata.User{Name: user.Name, TenantID: user.TenantID, SpaceUID: "space-b"}, query: "query"},
		{name: "different_user", user: metadata.User{Name: "other", TenantID: user.TenantID, SpaceUID: user.SpaceUID}, query: "query"},
		{name: "different_query", user: user, query: "other-query"},
	} {
		testContext.Run(testCase.name, func(testContext *testing.T) {
			require.Equal(testContext, testCase.same, key == scrollSessionKey(&testCase.user, testCase.query))
		})
	}
}

func TestScrollSliceKey(testContext *testing.T) {
	for _, testCase := range []struct {
		name       string
		storage    string
		source     string
		wantSuffix string
	}{
		{name: "native_es", storage: metadata.ElasticsearchStorageType, source: structured.BkLog},
		{name: "default_es", storage: metadata.ElasticsearchStorageType},
		{name: "bkdata_es", storage: metadata.ElasticsearchStorageType, source: structured.BkData, wantSuffix: "|bkdata"},
		{name: "doris", storage: metadata.BkSqlStorageType, source: structured.BkData},
	} {
		testContext.Run(testCase.name, func(testContext *testing.T) {
			query := &metadata.Query{TableID: "same-table", StorageID: "same-storage", SliceID: "0", StorageType: testCase.storage, SourceType: testCase.source}
			require.Equal(testContext, query.TableUUID()+testCase.wantSuffix, scrollSliceKey(query))
		})
	}
}
