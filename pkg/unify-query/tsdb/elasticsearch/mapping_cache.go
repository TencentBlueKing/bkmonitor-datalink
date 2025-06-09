// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
// http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under
// the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
// either express or implied. See the License for the specific language governing permissions and
// limitations under the License.

package elasticsearch

import (
	"context"
	"fmt"
	"strings"

	"github.com/dgraph-io/ristretto/v2"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type FieldTypesCache interface {
	GetAliasMappings(ctx context.Context, alias []string) ([]map[string]any, bool)
	SetAliasMappings(ctx context.Context, alias []string, mappings []map[string]any)
	ClearFieldTypesCache()
}

type MappingCache struct {
	fieldTypesCache *ristretto.Cache[string, []map[string]any]
}

func (m *MappingCache) ClearFieldTypesCache() {
	m.fieldTypesCache.Clear()
}

func NewMappingCache() (cache FieldTypesCache, err error) {
	c, err := ristretto.NewCache(&ristretto.Config[string, []map[string]any]{
		MaxCost:     viper.GetInt64(MappingCacheMaxCostPath),
		NumCounters: viper.GetInt64(MappingCacheNumCountersPath),
		BufferItems: viper.GetInt64(MappingCacheBufferItemsPath),
		Cost: func(value []map[string]any) int64 {
			return int64(len(value))
		},
		IgnoreInternalCost: false,
	})
	if err != nil {
		return
	}

	return &MappingCache{
		fieldTypesCache: c,
	}, nil
}

func (m *MappingCache) SetAliasMappings(ctx context.Context, alias []string, mappings []map[string]any) {
	var err error
	ctx, span := trace.NewSpan(ctx, "set-alias-mappings")
	defer span.End(&err)

	span.Set("alias", fmt.Sprintf("%v", alias))
	span.Set("mappings_count", fmt.Sprintf("%d", len(mappings)))

	if len(mappings) == 0 {
		return
	}

	ttl := viper.GetDuration(MappingCacheTTLPath)
	key := strings.Join(alias, ",")
	success := m.fieldTypesCache.SetWithTTL(key, mappings, int64(len(mappings)), ttl)
	if !success {
		return
	}
	m.fieldTypesCache.Wait()
}

func (m *MappingCache) GetAliasMappings(ctx context.Context, alias []string) ([]map[string]any, bool) {
	var err error
	ctx, span := trace.NewSpan(ctx, "get-alias-mappings")
	defer span.End(&err)
	span.Set("alias", fmt.Sprintf("%v", alias))
	key := strings.Join(alias, ",")
	mapping, found := m.fieldTypesCache.Get(key)
	if !found {
		span.Set("cache_hit", "false")
		return nil, false
	}
	span.Set("cache_hit", "true")
	span.Set("fields_count", fmt.Sprintf("%d", len(mapping)))
	return mapping, true
}

var fieldTypesCache FieldTypesCache
