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

type savedProcessor struct {
	Name    string               `json:"-"`
	Status  domain.EnrichStatus  `json:"status"`
	Patches []domain.EnrichPatch `json:"patches"`
	Value   domain.JSONObject    `json:"value"`
}

// savedProcessors 检查顺序与信封，只返回可应用的处理器；不解释无关业务值。
func savedProcessors(payload domain.JSONObject) ([]savedProcessor, error) {
	if len(payload) > 0 && (len(payload) != 1 || payload["processors"] == nil) {
		return nil, fmt.Errorf("invalid enrich payload")
	}
	var entries []map[string]savedProcessor
	if raw, ok := payload["processors"]; ok {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, err
		}
	}
	seen := map[string]bool{}
	result := make([]savedProcessor, 0, len(entries))
	for _, entry := range entries {
		if len(entry) != 1 {
			return nil, fmt.Errorf("enrich processor entry requires one key")
		}
		for name, processor := range entry {
			if seen[name] {
				return nil, fmt.Errorf("duplicate enrich processor")
			}
			seen[name] = true
			if processor.Status != domain.EnrichStatusSucceeded && processor.Status != domain.EnrichStatusPartial && processor.Status != domain.EnrichStatusFailed && processor.Status != domain.EnrichStatusSkipped {
				return nil, fmt.Errorf("invalid processor status")
			}
			if processor.Status == domain.EnrichStatusFailed || processor.Status == domain.EnrichStatusSkipped {
				continue
			}
			processor.Name = name
			result = append(result, processor)
		}
	}
	return result, nil
}

// EffectiveEnrichPatches 校验处理器信封并提取可应用补丁；真实历史 value 在此处投影。
func EffectiveEnrichPatches(payload domain.JSONObject) ([]domain.EnrichPatch, error) {
	processors, err := savedProcessors(payload)
	if err != nil {
		return nil, err
	}
	all := []domain.EnrichPatch{}
	for _, result := range processors {
		patches := result.Patches
		if patches == nil && result.Value != nil {
			patches, err = kingeye.ProjectValue(result.Name, result.Value)
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
	return all, nil
}

// EnrichedLabel 从已保存结果提取一个有效标签，不校验或解码其他 Patch 的值。
// 覆盖顺序、失败步骤跳过及历史 value 的标量映射与完整视图一致。
func EnrichedLabel(labels domain.DimensionMap, payload domain.JSONObject, key string) (domain.Scalar, bool, error) {
	processors, err := savedProcessors(payload)
	if err != nil {
		return domain.Scalar{}, false, err
	}
	value, exists := labels[key]
	count := 0
	for _, result := range processors {
		if result.Patches == nil {
			// 旧 display 的三个展示字段不属于 labels，其余仅标量生成标签补丁。
			if result.Name == "display" && (key == "title" || key == "content" || key == "object") {
				continue
			}
			var scalar domain.Scalar
			if raw, ok := result.Value[key]; ok && json.Unmarshal(raw, &scalar) == nil {
				value, exists = scalar, true
			}
			continue
		}
		count += len(result.Patches)
		if count > 4096 {
			return domain.Scalar{}, false, fmt.Errorf("enrich patch count exceeded")
		}
		for _, patch := range result.Patches {
			target, err := jsonpath.ParseTarget(patch.Path)
			if err != nil {
				return domain.Scalar{}, false, err
			}
			parts := target.Parts()
			if parts[0] != "labels" {
				continue
			}
			if len(parts) != 2 {
				return domain.Scalar{}, false, fmt.Errorf("invalid label patch target")
			}
			label, ok := parts[1].(string)
			if !ok || label == "" {
				return domain.Scalar{}, false, fmt.Errorf("invalid label patch key")
			}
			if label != key {
				continue
			}
			if patch.Op != "set" {
				return domain.Scalar{}, false, fmt.Errorf("unsupported label patch operation")
			}
			if err := json.Unmarshal(patch.Value, &value); err != nil {
				return domain.Scalar{}, false, err
			}
			exists = true
		}
	}
	if exists {
		if err := (domain.DimensionMap{key: value}).Validate(); err != nil {
			return domain.Scalar{}, false, err
		}
	}
	return value, exists, nil
}

// EnrichedEvent 返回指定等级的有效视图；没有该等级的已保存结果时明确报错。
func EnrichedEvent(event domain.Event, severity string) (domain.Event, error) {
	result, ok := event.ForSeverity(severity)
	if !ok {
		return domain.Event{}, fmt.Errorf("event has no enrichment for requested severity")
	}
	var evaluation domain.EventEvaluation
	for _, candidate := range event.Evaluations {
		if candidate.Severity == severity {
			evaluation = candidate
		}
	}
	document, err := domain.EventDocument(event, evaluation)
	if err != nil {
		return domain.Event{}, err
	}
	patches, err := EffectiveEnrichPatches(result.Data)
	if err != nil {
		return domain.Event{}, err
	}
	document, err = domain.ApplyEnrichPatches(document, patches)
	if err != nil {
		return domain.Event{}, err
	}
	data, err := json.Marshal(document)
	if err != nil {
		return domain.Event{}, err
	}
	var effective domain.Event
	if err := json.Unmarshal(data, &effective); err != nil {
		return domain.Event{}, err
	}
	effective.EventEnrichment = event.EventEnrichment.Clone()
	effective.RelatedAlertIDs = append([]string(nil), event.RelatedAlertIDs...)
	return effective, nil
}
