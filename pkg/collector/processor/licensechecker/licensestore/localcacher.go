// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package licensestore

import (
	"time"

	"github.com/patrickmn/go-cache"
)

const (
	defaultExpiration = time.Second * 60 // 官方文档中 skywalking 心跳数据每 30 秒上报一次
)

type localCacher struct {
	cache *cache.Cache
}

func newLocalCacher() Cacher {
	return &localCacher{cache: cache.New(time.Minute*2, time.Minute)}
}

func (c *localCacher) Set(value string) {
	c.cache.Set(value, value, defaultExpiration)
}

func (c *localCacher) Exist(key string) bool {
	_, ok := c.cache.Get(key)
	return ok
}

func (c *localCacher) Items() []string {
	items := c.cache.Items()
	result := make([]string, 0, len(items))
	for _, value := range items {
		result = append(result, value.Object.(string))
	}
	return result
}

func (c *localCacher) Count() int {
	return len(c.cache.Items())
}
