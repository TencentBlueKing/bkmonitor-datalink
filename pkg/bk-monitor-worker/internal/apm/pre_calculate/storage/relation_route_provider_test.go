package storage

import (
	"context"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestRelationRouteProviderReload(t *testing.T) {
	server := miniredis.RunT(t)
	client := goRedis.NewClient(&goRedis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	provider := &relationRouteProviderImpl{
		ctx:                  context.Background(),
		client:               client,
		resultTableDetailKey: "result_table_detail",
		builtInDetailKey:     "built_in_result_table_detail",
		ready:                make(map[string]bool),
	}
	require.NoError(t, client.HSet(
		context.Background(),
		provider.builtInDetailKey,
		"bkcc__7",
		`{"token":"token-7","table_id":"7_bkcc_built_in_time_series.__default__"}`,
	).Err())
	require.NoError(t, client.HSet(
		context.Background(),
		provider.resultTableDetailKey,
		"7_bkcc_built_in_time_series.__default__",
		`{"storage_type":"victoria_metrics","surrealdb":{"storage_id":2,"database":"7_relation","namespace":"mapleleaf_7"}}`,
	).Err())

	require.NoError(t, provider.reload())
	require.True(t, provider.Ready("bkcc__7"))

	require.NoError(t, client.HDel(
		context.Background(), provider.resultTableDetailKey, "7_bkcc_built_in_time_series.__default__",
	).Err())
	require.NoError(t, provider.reload())
	require.False(t, provider.Ready("bkcc__7"))
}

func TestHasSurrealDBRoute(t *testing.T) {
	require.True(t, hasSurrealDBRoute(
		`{"storage_type":"surrealdb","storage_id":2,"database":"relation","namespace":"mapleleaf_7"}`,
	))
	require.True(t, hasSurrealDBRoute(
		`{"storage_type":"victoria_metrics","surrealdb":{"storage_id":2,"db":"relation","namespace":"mapleleaf_7"}}`,
	))
	require.False(t, hasSurrealDBRoute(
		`{"storage_type":"victoria_metrics","surrealdb":{"storage_id":2,"database":"relation"}}`,
	))
}
