// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"fmt"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

const (
	FormatESStep     = "."
	FormatProperties = "properties"

	FormatPropertiesType       = "type"
	FormatPropertiesDocValue   = "doc_values"
	FormatPropertiesNormalizer = "normalizer"
)

type IndexOptionFormat struct {
	analyzer map[string]map[string]any
	fieldMap map[string]map[string]any

	fieldAlias metadata.FieldAlias
}

func NewIndexOptionFormat(fieldAlias map[string]string) *IndexOptionFormat {
	return &IndexOptionFormat{
		analyzer:   make(map[string]map[string]any),
		fieldMap:   make(map[string]map[string]any),
		fieldAlias: fieldAlias,
	}

}

func (f *IndexOptionFormat) FieldMap() map[string]map[string]any {
	return f.fieldMap
}

func (f *IndexOptionFormat) Parse(settings, mappings map[string]any) {
	// 解析 settings 里面的 analysis
	if analysis, ok := settings["analysis"].(map[string]any); ok {
		tokenizer, _ := analysis["tokenizer"].(map[string]any)
		analyzer, _ := analysis["analyzer"].(map[string]any)

		for k, v := range analyzer {
			if nv, ok := v.(map[string]any); ok {
				f.analyzer[k] = nv
				if ck, ok := nv["tokenizer"].(string); ok && ck != "" {
					if cv, ok := tokenizer[ck]; ok {
						if ncv, ok := cv.(map[string]any); ok {
							for tk, tv := range ncv {
								f.analyzer[k][tk] = tv
							}
						}
					}
				}
			}
		}
	}

	if _, ok := mappings[FormatProperties]; ok {
		f.mapMappings("", mappings)
	} else {
		// 有的 es 因为版本不同，properties 不在第一层，所以需要往下一层找
		for _, m := range mappings {
			switch nm := m.(type) {
			case map[string]any:
				f.Parse(settings, nm)
			}
		}
	}
}

func (f *IndexOptionFormat) mapMappings(prefix string, data map[string]any) {
	if data == nil {
		return
	}

	if properties, ok := data[FormatProperties].(map[string]any); ok {
		for k, v := range properties {
			if prefix != "" {
				k = fmt.Sprintf("%s%s%s", prefix, FormatESStep, k)
			}

			switch nv := v.(type) {
			case map[string]any:
				f.mapMappings(k, nv)
			}
		}
	}

	if prefix != "" {
		if _, ok := f.fieldMap[prefix]; ok {
			return
		}

		fm := f.esToFieldMap(prefix, data)
		if fm != nil {
			f.fieldMap[prefix] = fm
		}
	}
}

func (f *IndexOptionFormat) setValue(k string, data map[string]any) any {
	if v, ok := data[k]; ok && v != nil {
		return v
	}

	return nil
}

func (f *IndexOptionFormat) esToFieldMap(k string, data map[string]any) map[string]any {
	if k == "" {
		return nil
	}
	if data["type"] == nil {
		return nil
	}

	fieldMap := make(map[string]any)
	fieldMap["alias_name"] = f.fieldAlias.AliasName(k)
	fieldMap["field_name"] = k
	fieldMap["field_type"] = data["type"]

	fieldMap["is_agg"] = false
	fieldMap["tokenize_on_chars"] = ""
	ks := strings.Split(k, ESStep)
	fieldMap["origin_field"] = ks[0]
	fieldMap["is_analyzed"] = false
	fieldMap["is_case_sensitive"] = false

	if v, ok := data["doc_values"].(bool); ok {
		fieldMap["is_agg"] = v
	}

	if t, ok := fieldMap["field_type"].(string); ok && t == Text {
		fieldMap["is_analyzed"] = true
	}

	if v, ok := data["normalizer"].(bool); ok {
		fieldMap["is_case_sensitive"] = v
	}

	if name, ok := data["analyzer"].(string); ok {
		analyzer := f.analyzer[name]
		if analyzer != nil {
			fieldMap["tokenize_on_chars"] = analyzer["tokenize_on_chars"]
		}
	}

	return fieldMap
}
