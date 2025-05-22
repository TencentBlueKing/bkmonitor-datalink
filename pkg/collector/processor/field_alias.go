// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processor

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

// AliasConfig 字段别名配置
type AliasConfig struct {
	Field string   `config:"field"`
	Alias []string `config:"alias"`
}

// FieldAliasMapper 字段别名映射
type FieldAliasMapper struct {
	m map[string][]string
}

func newMapper() *FieldAliasMapper {
	return &FieldAliasMapper{
		m: nil,
	}
}

// LoadAlias 从 field_alias 项加载配置
func LoadAlias(conf *confengine.Config) error {
	var cfgs []AliasConfig
	err := conf.UnpackChild(define.ConfigFieldAlias, &cfgs)
	if err != nil {
		return err
	}
	attrMap := make(map[string][]string)
	resMap := make(map[string][]string)
	for _, cfg := range cfgs {
		df, k := DecodeDimensionFrom(cfg.Field)
		switch df {
		case DimensionFromAttribute:
			aliases := []string{k}
			for _, alias := range cfg.Alias {
				_, a := DecodeDimensionFrom(alias)
				aliases = append(aliases, a)
			}
			attrMap[k] = aliases
		case DimensionFromResource:
			aliases := []string{k}
			for _, alias := range cfg.Alias {
				_, a := DecodeDimensionFrom(alias)
				aliases = append(aliases, a)
			}
			resMap[k] = aliases
		default:
			continue
		}
	}
	// 直接替换指针，原子操作避免并发读写
	AttributeAlias.m = attrMap
	ResourceAlias.m = resMap
	return nil
}

// Get 获取包含原字段的字段别名，未配置则返回原字段
func (d *FieldAliasMapper) Get(key string) []string {
	if v, ok := d.m[key]; ok && len(v) > 0 {
		return v
	}
	// 不存在映射配置则返回 key 本身
	return []string{key}
}

// AttributeAlias 全局配置
var AttributeAlias = newMapper()

var ResourceAlias = newMapper()
