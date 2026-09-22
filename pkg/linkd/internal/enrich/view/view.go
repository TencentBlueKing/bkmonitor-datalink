// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package view 根据已保存的步骤输出合成告警视图，不执行查询、规则或写入。
package view

import (
	"encoding/json"
	"fmt"

	"linkd/internal/domain"
	"linkd/internal/enrich/kingeye"
	"linkd/internal/jsonpath"
)

// EnrichedAlert 返回按处理器顺序合成的临时视图；原始 domain.Alert 和存储内容均不改变。
func EnrichedAlert(alert domain.Alert) (domain.Alert, error) {
	document, err := domain.AlertDocument(alert)
	if err != nil {
		return domain.Alert{}, err
	}
	patches, err := EffectiveEnrichPatches(alert.Enrich)
	if err != nil {
		return domain.Alert{}, err
	}
	document, err = domain.ApplyEnrichPatches(document, patches)
	if err != nil {
		return domain.Alert{}, err
	}
	data, err := json.Marshal(document)
	if err != nil {
		return domain.Alert{}, err
	}
	var result domain.Alert
	if err := json.Unmarshal(data, &result); err != nil {
		return domain.Alert{}, err
	}
	result.Enrich = alert.Enrich.Clone()
	result.EnrichStatus = alert.EnrichStatus
	return result, nil
}

// EffectiveEnrichPatches 校验处理器信封并提取可应用补丁；真实历史 value 在此处投影。
func EffectiveEnrichPatches(payload domain.JSONObject) ([]domain.EnrichPatch, error) {
	var err error
	all := []domain.EnrichPatch{}
	if len(payload) > 0 {
		if len(payload) != 1 || payload["processors"] == nil {
			return nil, fmt.Errorf("invalid enrich payload")
		}
	}
	var entries []map[string]struct {
		Status  domain.EnrichStatus  `json:"status"`
		Patches []domain.EnrichPatch `json:"patches"`
		Value   domain.JSONObject    `json:"value"`
	}
	if raw, ok := payload["processors"]; ok {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, err
		}
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if len(entry) != 1 {
			return nil, fmt.Errorf("enrich processor entry requires one key")
		}
		for name, result := range entry {
			if seen[name] {
				return nil, fmt.Errorf("duplicate enrich processor")
			}
			seen[name] = true
			if result.Status != domain.EnrichStatusSucceeded && result.Status != domain.EnrichStatusPartial && result.Status != domain.EnrichStatusFailed && result.Status != domain.EnrichStatusSkipped {
				return nil, fmt.Errorf("invalid processor status")
			}
			if result.Status == domain.EnrichStatusFailed || result.Status == domain.EnrichStatusSkipped {
				continue
			}
			patches := result.Patches
			if patches == nil && result.Value != nil {
				patches, err = kingeye.ProjectValue(name, result.Value)
				if err != nil {
					return nil, err
				}
			}
			for _, patch := range patches {
				if err := patch.Validate(); err != nil {
					return nil, err
				}
			}
			all = append(all, patches...)
			if len(all) > 4096 {
				return nil, fmt.Errorf("enrich patch count exceeded")
			}
		}
	}
	return all, nil
}

// EnrichedLabels 为窄索引读取计算标签，不要求读取正文或 extra_data 中的数组父节点。
func EnrichedLabels(labels domain.DimensionMap, payload domain.JSONObject) (domain.DimensionMap, error) {
	patches, err := EffectiveEnrichPatches(payload)
	if err != nil {
		return nil, err
	}
	out := labels.Normalize()
	for _, patch := range patches {
		target, _ := jsonpath.ParseTarget(patch.Path)
		parts := target.Parts()
		if len(parts) != 2 || parts[0] != "labels" {
			continue
		}
		var scalar domain.Scalar
		if err := json.Unmarshal(patch.Value, &scalar); err != nil {
			return nil, err
		}
		out[parts[1].(string)] = scalar
	}
	return out, nil
}
