package metadata

import (
	"context"
	"testing"
	"time"

	cache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"
)

func TestQueryMetadataOutlivesGlobalCacheWithoutRetainingChildren(t *testing.T) {
	InitMetadata()
	ctx := InitHashID(context.Background())
	SetUser(ctx, &User{Key: "source:test", TenantID: "tenant", SpaceUID: "bkcc__7"})
	GetQueryParams(ctx).SetIsSkipK8s(true)
	scoped := WithQueryMetadata(ctx)
	// Expire exactly the global state without sleeping or changing settings.
	md.c.Set(hashID(ctx)+"_"+UserKey, GetUser(ctx), time.Nanosecond)
	md.c.Set(hashID(ctx)+"_"+QueryParamsKey, GetQueryParams(ctx), time.Nanosecond)
	time.Sleep(time.Millisecond)
	require.Empty(t, GetUser(ctx).TenantID)
	require.False(t, GetQueryParams(ctx).IsSkipK8s)
	require.Equal(t, "tenant", GetUser(scoped).TenantID)
	require.True(t, GetQueryParams(scoped).IsSkipK8s)
	globalCount := md.c.ItemCount()
	child := WithQueryMetadata(InitHashID(scoped))
	SetUser(child, &User{TenantID: "child"})
	GetQueryParams(child).SetIsSkipK8s(true)
	SetExpand(child, &VmExpand{})
	require.Equal(t, globalCount, md.c.ItemCount(), "child query metadata must not be retained globally")
	require.Equal(t, "tenant", GetUser(scoped).TenantID)
	require.Equal(t, "child", GetUser(child).TenantID)
	require.NotSame(t, scoped.Value(queryMetadataContextKey{}), child.Value(queryMetadataContextKey{}))
	owned := child.Value(queryMetadataContextKey{}).(*cache.Cache)
	_, expiry, found := owned.GetWithExpiration(hashID(child) + "_" + UserKey)
	require.True(t, found)
	require.True(t, expiry.IsZero(), "live query metadata follows context lifetime")
}

func TestQueryMetadataPreservesOutputStatusAndCopiesMutableParams(t *testing.T) {
	InitMetadata()
	ctx := InitHashID(context.Background())
	SetUser(ctx, &User{TenantID: "base"})
	GetQueryParams(ctx).SetTime(time.Unix(1, 0), time.Unix(1, 0), time.Unix(2, 0), time.Minute, "s", "UTC").SetStorageType(InfluxDBStorageType)
	output := WithNamedOutputScope(WithStatusScope(ctx, 2), 2)
	SetStatus(output, QueryTsPartial, "route partial")
	scoped := WithQueryMetadata(output)
	require.Equal(t, "base", GetUser(scoped).TenantID)
	require.Equal(t, GetStatus(output), GetStatus(scoped))
	require.Equal(t, time.Minute, GetQueryParams(scoped).Step)
	GetQueryParams(scoped).SetStorageType(VictoriaMetricsStorageType).SetIsSkipK8s(true)
	GetUser(scoped).TenantID = "owned"
	SetStatus(scoped, "OWNED", "owned status")
	require.False(t, GetQueryParams(output).IsSkipK8s)
	require.False(t, GetQueryParams(output).IsDirectQuery())
	require.Equal(t, "base", GetUser(output).TenantID)
	require.Equal(t, QueryTsPartial, GetStatus(output).Code)
	nested := WithNamedOutputScope(scoped, 3)
	require.Equal(t, "owned", GetUser(nested).TenantID, "output fallback retains request user")
	SetExpand(nested, &VmExpand{})
	require.Nil(t, GetExpand(scoped))
}
