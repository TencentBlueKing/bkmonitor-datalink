// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

// ElasticSearchMetaClusterInfo :
type ElasticSearchMetaClusterInfo struct {
	*SimpleMetaClusterInfo
}

// GetIndex :
func (c *ElasticSearchMetaClusterInfo) GetIndex() string {
	return c.StorageConfigHelper.MustGetString("base_index")
}

// SetIndex :
func (c *ElasticSearchMetaClusterInfo) SetIndex(index string) {
	c.StorageConfigHelper.Set("base_index", index)
}

// GetVersion :
func (c *ElasticSearchMetaClusterInfo) GetVersion() string {
	version, ok := c.StorageConfigHelper.GetString("version")
	if ok {
		return version
	}
	return c.ClusterConfigHelper.MustGetString("version")
}

// SetVersion :
func (c *ElasticSearchMetaClusterInfo) SetVersion(val string) {
	c.StorageConfigHelper.Set("version", val)
}

// GetTarget :
func (c *ElasticSearchMetaClusterInfo) GetTarget() string {
	return c.GetIndex()
}

// AsElasticSearchCluster :
func (c *MetaClusterInfo) AsElasticSearchCluster() *ElasticSearchMetaClusterInfo {
	info := NewSimpleMetaClusterInfo(c)
	return &ElasticSearchMetaClusterInfo{
		SimpleMetaClusterInfo: info,
	}
}
