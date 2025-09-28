// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package alias

import (
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func ServerKind(s string) KindKey {
	return KindKey{Kind: "SPAN_KIND_SERVER", Key: s} // 2
}

func ClientKind(s string) KindKey {
	return KindKey{Kind: "SPAN_KIND_CLIENT", Key: s} // 3
}

type KindKey struct {
	Kind string
	Key  string
}

type KF struct {
	K KindKey
	F LookupFunc
}

type LookupFunc func(span ptrace.Span) (string, bool)

func FuncContact(l, r, op string) LookupFunc {
	return func(span ptrace.Span) (string, bool) {
		attrs := span.Attributes()
		lv, ok := attrs.Get(l)
		if !ok {
			return "", false
		}
		rv, ok := attrs.Get(r)
		if !ok {
			return "", false
		}
		return lv.AsString() + op + rv.AsString(), true
	}
}

func FuncOr(keys ...string) LookupFunc {
	return func(span ptrace.Span) (string, bool) {
		attrs := span.Attributes()
		for _, k := range keys {
			if v, ok := attrs.Get(k); ok {
				return v.AsString(), true
			}
		}
		return "", false
	}
}

type Manager struct {
	attributes map[KindKey]LookupFunc
}

func New() *Manager {
	return &Manager{
		attributes: map[KindKey]LookupFunc{},
	}
}

// Register 注册 KFs
//
// 线程不安全 调用方需自己保证并发安全
func (m *Manager) Register(kfs ...KF) {
	for _, kf := range kfs {
		m.attributes[kf.K] = kf.F
	}
}

// GetAttributes 获取属性值
//
// 优先从原 span 中的 attributes 中获取 当且仅当原 span 不存在才从 alias 中加载
func (m *Manager) GetAttributes(span ptrace.Span, key string) (string, bool) {
	kk := KindKey{
		Kind: span.Kind().String(),
		Key:  key,
	}

	// 优先从 span 中获取确定的 key
	if v, ok := span.Attributes().Get(key); ok {
		return v.AsString(), true
	}

	// 当原有的 key 不存在的时候才进一步从 attributes 中或者
	f, ok := m.attributes[kk]
	if !ok {
		return "", false // 如若不存在则放弃继续检索
	}
	return f(span)
}
