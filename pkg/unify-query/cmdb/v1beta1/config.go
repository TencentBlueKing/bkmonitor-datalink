// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta1

import (
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

var configData = buildConfigData()

func buildConfigData() *Config {
	resources := make([]ResourceConf, 0, len(relation.DefaultResourceDefinitions()))
	for _, rd := range relation.DefaultResourceDefinitions() {
		var index, info cmdb.Index
		for _, f := range rd.Fields {
			if f.Required {
				index = append(index, f.Name)
			} else {
				info = append(info, f.Name)
			}
		}
		resources = append(resources, ResourceConf{
			Name:  cmdb.Resource(rd.Name),
			Index: index,
			Info:  info,
		})
	}

	relations := make([]RelationConf, 0, len(relation.DefaultRelationDefinitions()))
	for _, rd := range relation.DefaultRelationDefinitions() {
		relations = append(relations, RelationConf{
			Resources: []cmdb.Resource{
				cmdb.Resource(rd.FromResource),
				cmdb.Resource(rd.ToResource),
			},
		})
	}

	return &Config{
		Resource: resources,
		Relation: relations,
	}
}

var resourceConfig = make(map[cmdb.Resource]ResourceConf)
var resourceConfigMu sync.RWMutex

// updateResourceConfig 更新资源配置映射
func updateResourceConfig(cfg *Config) {
	if cfg == nil || len(cfg.Resource) == 0 {
		cfg = configData
	}
	resourceConfigMu.Lock()
	defer resourceConfigMu.Unlock()
	newConfig := make(map[cmdb.Resource]ResourceConf, len(cfg.Resource))
	for _, c := range cfg.Resource {
		newConfig[c.Name] = c
	}
	resourceConfig = newConfig
}

func ResourcesIndex(resources ...cmdb.Resource) cmdb.Index {
	resourceConfigMu.RLock()
	defer resourceConfigMu.RUnlock()
	var index cmdb.Index
	for _, r := range resources {
		index = append(index, resourceConfig[r].Index...)
	}
	return index
}

func ResourcesInfo(resources ...cmdb.Resource) cmdb.Index {
	resourceConfigMu.RLock()
	defer resourceConfigMu.RUnlock()
	var index []string
	for _, r := range resources {
		index = append(index, resourceConfig[r].Info...)
	}
	return index
}

func AllResources() map[cmdb.Resource]ResourceConf {
	resourceConfigMu.RLock()
	defer resourceConfigMu.RUnlock()
	result := make(map[cmdb.Resource]ResourceConf, len(resourceConfig))
	for k, v := range resourceConfig {
		result[k] = v
	}
	return result
}
