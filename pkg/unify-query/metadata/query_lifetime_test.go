package metadata

import (
	"context"
	"fmt"
	"testing"
	"time"

	cache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
)

func TestQueryMetadataRetainsEverySupportedKey(t *testing.T) {
	for _, named := range []bool{false, true} {
		t.Run(fmt.Sprintf("named=%t", named), func(t *testing.T) {
			InitMetadata()
			base := InitHashID(context.Background())
			parent := WithStatusScope(base, 7)
			keys := []string{
				UserKey, StatusKey, statusKey(parent), ExpandKey, CheckPreviewMetricQLKey,
				QueriesKey, QueryParamsKey, QueryReferenceKey, QueryClusterMetricKey,
				JwtPayLoadKey, FieldFormatKey, HandlerMapKey,
			}
			populate := func(ctx context.Context, prefix string) {
				for key, value := range map[string]any{
					UserKey:                 &User{SpaceUID: prefix},
					StatusKey:               &Status{Code: prefix},
					statusKey(parent):       &Status{Code: prefix + "-scoped"},
					ExpandKey:               &VmExpand{ClusterName: prefix},
					CheckPreviewMetricQLKey: prefix,
					QueriesKey:              &Queries{ctx: ctx, directlyClusterID: prefix},
					QueryParamsKey:          &QueryParams{ctx: ctx, Timezone: prefix, StorageType: set.New[string]()},
					QueryReferenceKey:       QueryReference{prefix: {}},
					QueryClusterMetricKey:   &QueryClusterMetric{MetricName: prefix},
					JwtPayLoadKey:           map[string]string{"sub": prefix},
					FieldFormatKey:          &FieldFormat{ctx: ctx},
					HandlerMapKey:           handlerMap,
				} {
					md.set(ctx, key, value)
				}
			}
			populate(base, "base")
			if named {
				parent = WithNamedOutputScope(parent, 2)
				populate(parent, "output")
			}
			owned := WithQueryMetadata(parent)
			ownedCache := md.cache(owned)
			for _, key := range keys {
				_, found := ownedCache.Get(hashID(base) + "_" + key)
				require.True(t, found, "base entry %s", key)
				if named {
					_, found = ownedCache.Get(hashID(base) + "_output:2_" + key)
					require.True(t, found, "output entry %s", key)
				}
			}
			md.c.Flush()
			prefix := "base"
			if named {
				prefix = "output"
			}
			require.Equal(t, prefix, GetUser(owned).SpaceUID)
			require.Equal(t, prefix, GetQueryParams(owned).Timezone)
			require.Equal(t, prefix+"-scoped", GetStatus(owned).Code)
			require.Equal(t, prefix, GetExpand(owned).ClusterName)
			require.Equal(t, prefix, GetCheckPreviewMetricQL(owned))
			require.Equal(t, prefix, GetQueries(owned).DirectlyClusterID())
			require.Contains(t, GetQueryReference(owned), prefix)
			require.Equal(t, prefix, GetQueryClusterMetric(owned).MetricName)
			require.Same(t, ownedCache, md.cache(GetFieldFormat(owned).ctx))
		})
	}
}

func TestQueryMetadataCopiesFieldFormatAndNilStorageSet(t *testing.T) {
	InitMetadata()
	parent := InitHashID(context.Background())
	format := GetFieldFormat(parent)
	params := GetQueryParams(parent)
	params.StorageType = nil
	owned := WithQueryMetadata(parent)
	ownedFormat := GetFieldFormat(owned)
	require.NotSame(t, format, ownedFormat)
	require.Same(t, md.cache(owned), md.cache(ownedFormat.ctx))
	require.NotSame(t, params, GetQueryParams(owned))
	GetQueryParams(owned).SetStorageType(InfluxDBStorageType)
	require.Nil(t, params.StorageType)
	require.True(t, GetQueryParams(owned).StorageType.Existed(InfluxDBStorageType))
	encoded := ownedFormat.EncodeFunc()("pod.name")
	require.Equal(t, "pod.name", ownedFormat.DecodeFunc()(encoded))
	md.c.Flush()
	require.Same(t, ownedFormat, ownedFormat.set())
	require.Same(t, ownedFormat, GetFieldFormat(owned))
	require.NotSame(t, ownedFormat, GetFieldFormat(parent))
}

func TestQueryMetadataBeforeInitialization(t *testing.T) {
	previous := md
	md = nil
	t.Cleanup(func() { md = previous })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.Same(t, ctx, WithQueryMetadata(ctx))
	require.Nil(t, GetStatus(ctx))
	cancel()
	require.ErrorIs(t, WithQueryMetadata(ctx).Err(), context.Canceled)
}

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
