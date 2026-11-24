// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.
package metadata

import (
	"context"

	cache "github.com/patrickmn/go-cache"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
)

type queryMetadataContextKey struct{}

// WithQueryMetadata keeps active query state with its context instead of the
// process-wide TTL cache. It creates no janitor and no global reference to the
// context, so query state becomes collectible when its caller releases it.
// Each invocation creates an independent store; InitHashID still isolates IDs.
func WithQueryMetadata(ctx context.Context) context.Context {
	if md == nil {
		return ctx
	}
	parentCache := md.cache(ctx)
	ownedCache := cache.New(cache.NoExpiration, 0)
	owned := context.WithValue(ctx, queryMetadataContextKey{}, ownedCache)
	baseOwned := context.WithValue(owned, namedOutputScopeContextKey{}, nil)
	copyEntry := func(key string, targetCtx context.Context) {
		value, ok := parentCache.Get(key)
		if !ok {
			return
		}
		switch value := value.(type) {
		case *User:
			copyOfUser := *value
			ownedCache.SetDefault(key, &copyOfUser)
		case *QueryParams:
			params := *value
			params.ctx = targetCtx
			params.StorageType = set.New[string]()
			if value.StorageType != nil {
				params.StorageType.Add(value.StorageType.ToArray()...)
			}
			ownedCache.SetDefault(key, &params)
		case *FieldFormat:
			format := *value
			format.ctx = targetCtx
			ownedCache.SetDefault(key, &format)
		default:
			ownedCache.SetDefault(key, value)
		}
	}
	id := hashID(ctx)
	scope, scoped := ctx.Value(namedOutputScopeContextKey{}).(string)
	for _, key := range []string{
		UserKey, StatusKey, statusKey(ctx), ExpandKey, CheckPreviewMetricQLKey,
		QueriesKey, QueryParamsKey, QueryReferenceKey, QueryClusterMetricKey,
		JwtPayLoadKey, FieldFormatKey, HandlerMapKey,
	} {
		copyEntry(id+"_"+key, baseOwned)
		if scoped {
			copyEntry(id+"_output:"+scope+"_"+key, owned)
		}
	}
	return owned
}

func (m *metaData) cache(ctx context.Context) *cache.Cache {
	if owned, ok := ctx.Value(queryMetadataContextKey{}).(*cache.Cache); ok {
		return owned
	}
	return m.c
}
