package relation

import (
	"context"
	"encoding/json"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestRedisProviderDerivesCustomRelationDefinition(t *testing.T) {
	server := miniredis.RunT(t)
	client := goRedis.NewClient(&goRedis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	resourceValue := func(name string) string {
		value, err := json.Marshal(ResourceDefinition{
			Namespace: NamespaceAll,
			Name:      name,
			Fields:    []FieldDefinition{{Name: "id", Required: true}},
		})
		require.NoError(t, err)
		return string(value)
	}
	customValue, err := json.Marshal(CustomRelationStatus{
		Namespace:    "bkcc__7",
		Name:         "relation-1",
		FromResource: "app_version",
		ToResource:   "git_commit",
	})
	require.NoError(t, err)
	require.NoError(t, client.HSet(ctx, RedisKeyPrefix+":"+KindResourceDefinition, NamespaceAll,
		`{"app_version":`+resourceValue("app_version")+`,"git_commit":`+resourceValue("git_commit")+`}`).Err())
	require.NoError(t, client.HSet(ctx, RedisKeyPrefix+":"+KindCustomRelationStatus, "bkcc__7",
		`{"relation-1":`+string(customValue)+`}`).Err())

	provider, err := NewRedisProvider(ctx, client, WithReconnectConfig(1, 1))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Close()) })

	definitions, err := provider.ListRelationDefinitions("bkcc__7")
	require.NoError(t, err)
	require.Len(t, definitions, 1)
	require.Equal(t, "app_version_with_git_commit_relation", definitions[0].GetRelationName())
	require.Equal(t, "app_version", definitions[0].FromResource)
	require.Equal(t, "git_commit", definitions[0].ToResource)
}

func TestRedisProviderFiltersCustomRelationWithoutResources(t *testing.T) {
	server := miniredis.RunT(t)
	client := goRedis.NewClient(&goRedis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	require.NoError(t, client.HSet(ctx, RedisKeyPrefix+":"+KindCustomRelationStatus, "bkcc__7",
		`{"relation-1":{"namespace":"bkcc__7","name":"relation-1","from_resource":"missing","to_resource":"git_commit"}}`).Err())
	provider, err := NewRedisProvider(ctx, client, WithReconnectConfig(1, 1))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Close()) })

	definitions, err := provider.ListRelationDefinitions("bkcc__7")
	require.NoError(t, err)
	require.Empty(t, definitions)
}
