// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"

	cache "github.com/patrickmn/go-cache"
)

// metaData 元数据存储
type metaData struct {
	c *cache.Cache
}

// Get 通过 traceID + key 获取缓存
func (m *metaData) get(ctx context.Context, key string) (any, bool) {
	id := hashID(ctx)
	k := id + "_" + key
	return m.c.Get(k)
}

// Set 通过 traceID + key 写入缓存
func (m *metaData) set(ctx context.Context, key string, value any) {
	id := hashID(ctx)
	k := id + "_" + key
	m.c.SetDefault(k, value)
}
