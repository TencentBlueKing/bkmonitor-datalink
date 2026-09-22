// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package kingeye 定义 Kingeye 集成所需的纯数据协议和内置结果映射，不建立外部连接。
package kingeye

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"linkd/internal/domain"
)

// ProjectValue 将既有内置处理器结果映射为 Alert 字段；也用于读取真实历史记录。
func ProjectValue(processor string, value domain.JSONObject) ([]domain.EnrichPatch, error) {
	patches := make([]domain.EnrichPatch, 0, len(value))
	// domain.JSONObject 遍历顺序不稳定，投影按字段名排序以保证补丁可重放。
	for _, key := range sortedObjectKeys(value) {
		raw := value[key]
		// 内置权限列表的空缺省值没有覆盖语义；自定义补丁仍可显式赋空列表。
		if (key == "cw_labels" || key == "dynamic_group_id") && bytes.Equal(bytes.TrimSpace(raw), []byte("[]")) {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		path := ""
		switch processor {
		case "display":
			switch key {
			case "title", "content":
				path = "$." + key
			case "object":
				path = "$.subject_name"
			}
		}
		if path == "" {
			var scalar domain.Scalar
			if json.Unmarshal(raw, &scalar) == nil {
				path = "$[\"labels\"][" + quotedField(key) + "]"
			} else {
				path = "$[\"extra_data\"][" + quotedField(key) + "]"
			}
		}
		// 缺省空展示值不覆盖原始标题和内容；显式用户 set 空字符串仍按其配置执行。
		if (path == "$.title" || path == "$.content" || path == "$.subject_name") && strings.TrimSpace(string(raw)) == "\"\"" {
			continue
		}
		p := domain.EnrichPatch{Op: "set", Path: path, Value: append(json.RawMessage(nil), raw...)}
		if err := p.Validate(); err != nil {
			return nil, err
		}
		patches = append(patches, p)
	}
	return patches, nil
}

func quotedField(s string) string { b, _ := json.Marshal(s); return string(b) }

func sortedObjectKeys(value domain.JSONObject) []string {
	keys := make([]string, 0, len(value))
	for k := range value {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
