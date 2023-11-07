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
)

// InfluxDBMetaClusterInfo :
type InfluxDBMetaClusterInfo struct {
	*SimpleMetaClusterInfo
}

// GetDataBase :
func (c *InfluxDBMetaClusterInfo) GetDataBase() string {
	return c.StorageConfigHelper.MustGetString("database")
}

// SetDataBase :
func (c *InfluxDBMetaClusterInfo) SetDataBase(value string) {
	c.StorageConfigHelper.Set("database", value)
}

// GetTable :
func (c *InfluxDBMetaClusterInfo) GetTable() string {
	return c.StorageConfigHelper.MustGetString("real_table_name")
}

// SetTable :
func (c *InfluxDBMetaClusterInfo) SetTable(value string) {
	c.StorageConfigHelper.Set("real_table_name", value)
}

// GetTable :
func (c *InfluxDBMetaClusterInfo) GetRetentionPolicy() string {
	return c.StorageConfigHelper.MustGetString("retention_policy_name")
}

// SetTable :
func (c *InfluxDBMetaClusterInfo) SetRetentionPolicy(value string) {
	c.StorageConfigHelper.Set("retention_policy_name", value)
}

// GetTarget :
func (c *InfluxDBMetaClusterInfo) GetTarget() string {
	return fmt.Sprintf("%s.%s", c.GetDataBase(), c.GetTable())
}

// AsInfluxCluster :
func (c *MetaClusterInfo) AsInfluxCluster() *InfluxDBMetaClusterInfo {
	return &InfluxDBMetaClusterInfo{
		SimpleMetaClusterInfo: NewSimpleMetaClusterInfo(c),
	}
}
