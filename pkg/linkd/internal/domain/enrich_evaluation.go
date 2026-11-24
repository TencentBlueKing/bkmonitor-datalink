// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain

import (
	"encoding/json"
	"fmt"

	"linkd/internal/jsonpath"
)

// EnrichEvaluation 是一个等级贯穿整条处理链的工作对象；来源事实和补丁记录始终独立。
// Document/OriginalDocument 只供包内可信读取方使用，写入必须经过 Apply。
type EnrichEvaluation struct {
	source                      *Event
	evaluation                  EventEvaluation
	title, content, subjectName string
	labels                      DimensionMap
	extraData                   map[string]any
	original, document          map[string]any
	originalMeasure             jsonpath.Measurement
	roots                       map[string]jsonpath.Measurement
	changedLabels               map[string]bool
	undo                        []func()
	frozen                      bool
	payload                     JSONObject
}

// NewEnrichEvaluation 为一个等级创建唯一工作对象，不编码完整 Event。
func NewEnrichEvaluation(source *Event, evaluation EventEvaluation) (*EnrichEvaluation, error) {
	if source.enrichViews != nil {
		facts := *source
		facts.enrichViews = nil
		source = &facts
	}
	original, err := EventDocument(*source, evaluation)
	if err != nil {
		return nil, err
	}
	document := make(map[string]any, len(original))
	for k, v := range original {
		document[k] = v
	}
	// 只有白名单允许修改的集合需要隔离；其他来源分支只读共享。
	for _, k := range []string{"labels", "extra_data"} {
		document[k] = jsonpath.Clone(original[k])
	}
	s, err := newEnrichEvaluation(original, document)
	if err != nil {
		return nil, err
	}
	s.source, s.evaluation = source, evaluation
	return s, nil
}

// NewEnrichDocumentEvaluation 为独立规则执行/预览创建隔离工作对象。
func NewEnrichDocumentEvaluation(original, current map[string]any) (*EnrichEvaluation, error) {
	return newEnrichEvaluation(jsonpath.Clone(original).(map[string]any), jsonpath.Clone(current).(map[string]any))
}

func newEnrichEvaluation(original, document map[string]any) (*EnrichEvaluation, error) {
	m, err := jsonpath.Measure(original)
	if err != nil {
		return nil, err
	}
	s := &EnrichEvaluation{original: original, document: document, originalMeasure: m, roots: map[string]jsonpath.Measurement{}, labels: DimensionMap{}}
	s.title, _ = document["title"].(string)
	s.content, _ = document["content"].(string)
	s.subjectName, _ = document["subject_name"].(string)
	s.extraData, _ = document["extra_data"].(map[string]any)
	if labels, ok := document["labels"].(map[string]any); ok {
		for key, value := range labels {
			scalar, err := ScalarFromValue(value)
			if err == nil {
				s.labels[key] = scalar
			}
		}
	}
	for key, value := range document {
		measured, err := jsonpath.Measure(value)
		if err != nil {
			return nil, err
		}
		s.roots[key] = measured
	}
	if err := s.Measurement().Check(); err != nil {
		return nil, err
	}
	return s, nil
}

// Document 返回当前只读查询视图；对象身份在整个 evaluation 内不变。
func (s *EnrichEvaluation) Document() map[string]any { return s.document }

// OriginalDocument 返回原始事实的只读查询视图。
func (s *EnrichEvaluation) OriginalDocument() map[string]any { return s.original }

// Labels 返回仅供内部查询身份使用的只读有效标签。
func (s *EnrichEvaluation) Labels() DimensionMap { return s.labels }

// Savepoint 返回局部撤销位置，可在同一 Processor 内嵌套操作级边界。
func (s *EnrichEvaluation) Savepoint() int { return len(s.undo) }

// Rollback 逆序撤销到保存点，恢复新建父节点、缺失/null 与类型化字段。
func (s *EnrichEvaluation) Rollback(point int) {
	for i := len(s.undo) - 1; i >= point; i-- {
		s.undo[i]()
	}
	s.undo = s.undo[:point]
}

// Commit 释放成功步骤的旧值，避免整个处理过程持续持有已废弃的大对象。
func (s *EnrichEvaluation) Commit() error {
	// 与旧处理器边界的 Scalar 恢复一致；补丁记录保留求值结果，后续处理器读取类型化标签。
	if s.source != nil && len(s.changedLabels) > 0 {
		labels := s.document["labels"].(map[string]any)
		for key := range s.changedLabels {
			value, err := s.labels[key].JSONValue()
			if err != nil {
				return err
			}
			native, err := jsonpath.Native(value)
			if err != nil {
				return err
			}
			labels[key] = native
		}
		measured, err := jsonpath.Measure(labels)
		if err != nil {
			return err
		}
		s.roots["labels"] = measured
		if err := s.Measurement().Check(); err != nil {
			return err
		}
	}
	s.changedLabels = nil
	s.undo = nil
	return nil
}

// Freeze 在持久化后复用结果前关闭写入；只读消费者可共享该对象。
func (s *EnrichEvaluation) Freeze() error {
	if err := s.Commit(); err != nil {
		return err
	}
	s.frozen = true
	return nil
}

// FreezeResult 将只读工作对象绑定到实际存储协议，防止结果被替换后误用旧视图。
func (s *EnrichEvaluation) FreezeResult(payload JSONObject) error {
	if err := s.Freeze(); err != nil {
		return err
	}
	s.payload = payload.Clone()
	return nil
}

// Measurement 复用未修改分支的计量结果，不遍历完整来源载荷。
func (s *EnrichEvaluation) Measurement() jsonpath.Measurement {
	m := jsonpath.Measurement{Bytes: 2, Nodes: 1}
	for key, v := range s.roots {
		m.Bytes += jsonpath.StringBytes(key) + 1 + v.Bytes
		m.Nodes += v.Nodes
		m.Depth = max(m.Depth, v.Depth+1)
	}
	if len(s.roots) > 0 {
		m.Bytes += len(s.roots) - 1
	}
	return m
}

// ValidateEnvironment 用缓存的原始/当前结构预算检查查询上下文，保持原有整体查询边界。
func (s *EnrichEvaluation) ValidateEnvironment(env map[string]any) error {
	m := jsonpath.Measurement{Bytes: 2, Nodes: 1}
	for key, value := range env {
		var v jsonpath.Measurement
		var err error
		switch key {
		case "original":
			v = s.originalMeasure
		case "event":
			v = s.Measurement()
		default:
			v, err = jsonpath.Measure(value)
		}
		if err != nil {
			return err
		}
		m.Bytes += jsonpath.StringBytes(key) + 1 + v.Bytes
		m.Nodes += v.Nodes
		m.Depth = max(m.Depth, v.Depth+1)
	}
	if len(env) > 0 {
		m.Bytes += len(env) - 1
	}
	return m.Check()
}

// Apply 原子修改一组已校验补丁；只复制新写入的可变值并记录受影响节点。
func (s *EnrichEvaluation) Apply(patches []EnrichAssignment) error {
	if s.frozen {
		return fmt.Errorf("enrich evaluation is frozen")
	}
	if len(patches) > 4096 {
		return fmt.Errorf("enrich patch count exceeded")
	}
	point := s.Savepoint()
	for _, patch := range patches {
		if err := s.apply(patch); err != nil {
			s.Rollback(point)
			return err
		}
	}
	return nil
}

func (s *EnrichEvaluation) apply(patch EnrichAssignment) error {
	parts := patch.target.Parts()
	if len(parts) == 0 {
		return fmt.Errorf("unprepared enrich assignment")
	}
	root := parts[0].(string)
	oldMeasure, hadRoot := s.roots[root]
	oldExtra := s.extraData
	oldTitle, oldContent, oldSubject := s.title, s.content, s.subjectName
	var labelKey string
	var oldLabel Scalar
	var hadLabel bool
	if root == "labels" {
		labelKey = parts[1].(string)
		oldLabel, hadLabel = s.labels[labelKey]
	}
	wasChanged := s.changedLabels[labelKey]
	undo, err := patch.target.SetWithUndo(s.document, jsonpath.Clone(patch.value))
	if err != nil {
		return err
	}
	s.undo = append(s.undo, func() {
		undo()
		if hadRoot {
			s.roots[root] = oldMeasure
		} else {
			delete(s.roots, root)
		}
		s.extraData = oldExtra
		s.title, s.content, s.subjectName = oldTitle, oldContent, oldSubject
		if labelKey != "" {
			if hadLabel {
				s.labels[labelKey] = oldLabel
			} else {
				delete(s.labels, labelKey)
			}
		}
		if labelKey != "" && !wasChanged {
			delete(s.changedLabels, labelKey)
		}
	})
	switch root {
	case "title":
		s.title = patch.value.(string)
	case "content":
		s.content = patch.value.(string)
	case "subject_name":
		s.subjectName = patch.value.(string)
	case "labels":
		scalar, err := ScalarFromValue(patch.value)
		if err != nil {
			return err
		}
		s.labels[labelKey] = scalar
		if s.changedLabels == nil {
			s.changedLabels = map[string]bool{}
		}
		s.changedLabels[labelKey] = true
	case "extra_data":
		s.extraData, _ = s.document[root].(map[string]any)
	}
	measured, err := jsonpath.Measure(s.document[root])
	if err != nil {
		return err
	}
	s.roots[root] = measured
	return s.Measurement().Check()
}

// EventSnapshot 为要求 domain.Event 的接口生成隔离结果；运行中的规则不调用此方法。
func (s *EnrichEvaluation) EventSnapshot() (Event, error) {
	if s.source == nil {
		return Event{}, fmt.Errorf("evaluation has no source Event")
	}
	e := s.source.Clone()
	e.Title, e.Content, e.SubjectName = s.title, s.content, s.subjectName
	e.Labels = s.labels.Clone()
	e.ExtraData = JSONObject{}
	for key, value := range s.extraData {
		encoded, err := json.Marshal(value)
		if err != nil {
			return Event{}, err
		}
		e.ExtraData[key] = encoded
	}
	return e, nil
}
