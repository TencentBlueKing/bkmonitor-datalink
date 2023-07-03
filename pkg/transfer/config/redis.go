// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"fmt"

	"github.com/cstockton/go-conv"
)

// RedisMetaClusterInfo :
type RedisMetaClusterInfo struct {
	*SimpleMetaClusterInfo
}

// GetIsSentinel :
func (c *RedisMetaClusterInfo) GetIsSentinel() bool {
	return conv.Bool(c.MustGetStorageConfig("is_sentinel"))
}

// SetIsSentinel :
func (c *RedisMetaClusterInfo) SetIsSentinel(val bool) {
	c.StorageConfig["is_sentinel"] = val
}

// GetMaster :
func (c *RedisMetaClusterInfo) GetMaster() string {
	return conv.String(c.StorageConfig["master_name"])
}

// SetMaster :
func (c *RedisMetaClusterInfo) SetMaster(val string) {
	c.StorageConfig["master_name"] = val
}

// GetKey : 队列名称
func (c *RedisMetaClusterInfo) GetKey() string {
	return conv.String(c.MustGetStorageConfig("key"))
}

// SetKey : 队列名称
func (c *RedisMetaClusterInfo) SetKey(val string) {
	c.StorageConfig["key"] = val
}

// GetDB : 连接server之后,要选择的db
func (c *RedisMetaClusterInfo) GetDB() int {
	return conv.Int(c.MustGetStorageConfig("db"))
}

// SetDB : 连接server之后,要选择的db
func (c *RedisMetaClusterInfo) SetDB(val int) {
	c.StorageConfig["db"] = val
}

// GetTarget :
func (c *RedisMetaClusterInfo) GetTarget() string {
	return fmt.Sprintf("%d.%s", c.GetDB(), c.GetKey())
}

// AsRedisCluster :
func (c *MetaClusterInfo) AsRedisCluster() *RedisMetaClusterInfo {
	return &RedisMetaClusterInfo{
		SimpleMetaClusterInfo: NewSimpleMetaClusterInfo(c),
	}
}
