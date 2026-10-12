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
	"bytes"
	"sync"
)

type eventEnrichViews struct {
	mu                         sync.Mutex
	tenant, source, id, digest string
	version                    int64
	data                       map[string]JSONObject
	views                      map[string]*EnrichEvaluation
}

// WithEnrichEvaluations 将已冻结的工作对象绑定到当前持久化结果；恢复时允许惰性构建。
// Clone 可共享只读结果；Normalize/Restore 清除运行时引用，避免跨消息缓存。
func (e Event) WithEnrichEvaluations(views map[string]*EnrichEvaluation) Event {
	if views == nil && e.enrichViews != nil && e.enrichViews.matches(e) {
		return e
	}
	cache := &eventEnrichViews{tenant: e.BKTenantID, source: e.EventSourceID, id: e.EventID, digest: e.EnrichConfigDigest, version: e.EventSourceVersion, data: map[string]JSONObject{}, views: map[string]*EnrichEvaluation{}}
	for _, result := range e.Enrich.Evaluations {
		cache.data[result.Severity] = result.Data.Clone()
		if view := views[result.Severity]; view != nil && view.frozen && view.source != nil && view.evaluation.Severity == result.Severity && equalEnrichObject(view.payload, result.Data) && view.source.BKTenantID == e.BKTenantID && view.source.EventSourceID == e.EventSourceID && view.source.EventID == e.EventID && view.source.EventSourceVersion == e.EventSourceVersion {
			cache.views[result.Severity] = view
		}
	}
	e.enrichViews = cache
	return e
}

// ReuseEnrichEvaluations 将同一事实/结果的运行时视图传递到存储返回的新快照，不跨冲突重读复用。
func (e Event) ReuseEnrichEvaluations(previous Event) Event {
	if previous.enrichViews != nil && previous.enrichViews.matches(e) {
		e.enrichViews = previous.enrichViews
	}
	return e
}

func (c *eventEnrichViews) matches(e Event) bool {
	if c.tenant != e.BKTenantID || c.source != e.EventSourceID || c.id != e.EventID || c.version != e.EventSourceVersion || c.digest != e.EnrichConfigDigest || len(c.data) != len(e.Enrich.Evaluations) {
		return false
	}
	for _, result := range e.Enrich.Evaluations {
		if !equalEnrichObject(c.data[result.Severity], result.Data) {
			return false
		}
	}
	return true
}

// EnrichEvaluationView 只复用同一事件、发布版本和已保存 payload 的工作对象。
// 缓存失效时交由读取边界恢复，不允许按当前配置重新运行 Enrich。
func (e Event) EnrichEvaluationView(severity string, build func() (*EnrichEvaluation, error)) (*EnrichEvaluation, error) {
	c := e.enrichViews
	if c == nil || c.tenant != e.BKTenantID || c.source != e.EventSourceID || c.id != e.EventID || c.version != e.EventSourceVersion || c.digest != e.EnrichConfigDigest {
		return build()
	}
	result, found := e.ForSeverity(severity)
	if !found || !equalEnrichObject(c.data[severity], result.Data) {
		return build()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if view := c.views[severity]; view != nil {
		return view, nil
	}
	view, err := build()
	if err == nil {
		c.views[severity] = view
	}
	return view, err
}

func equalEnrichObject(left, right JSONObject) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if !bytes.Equal(value, right[key]) {
			return false
		}
	}
	return true
}
