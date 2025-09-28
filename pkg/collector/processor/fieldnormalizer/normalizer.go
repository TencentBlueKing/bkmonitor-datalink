// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fieldnormalizer

import (
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/fields"
)

type funcKey struct {
	PredicateKey string
	Kind         string
	Key          string
}

const (
	funcOr      = "or"
	funcContact = "contact"
)

type NormalizeFunc func(span ptrace.Span, key string)

func FuncContact(l, r, op string) NormalizeFunc {
	return func(span ptrace.Span, key string) {
		attrs := span.Attributes()
		if _, ok := attrs.Get(key); ok {
			return
		}

		lv, ok := attrs.Get(l)
		if !ok {
			return
		}
		rv, ok := attrs.Get(r)
		if !ok {
			return
		}
		attrs.InsertString(key, lv.AsString()+op+rv.AsString())
	}
}

func FuncOr(keys ...string) NormalizeFunc {
	return func(span ptrace.Span, key string) {
		attrs := span.Attributes()
		if _, ok := attrs.Get(key); ok {
			return
		}

		for _, k := range keys {
			if v, ok := attrs.Get(k); ok {
				attrs.Insert(key, v)
				return
			}
		}
	}
}

type SpanFieldNormalizer struct {
	ch    *ConfigHandler
	funcs map[funcKey]NormalizeFunc
}

func NewSpanFieldNormalizer(conf Config) *SpanFieldNormalizer {
	ch := NewConfigHandler(conf)
	funcs := make(map[funcKey]NormalizeFunc)
	for _, field := range conf.Fields {
		for _, rule := range field.Rules {
			// TODO(mando): 目前仅支持 Attributes 类型的字段
			ff, v := fields.DecodeFieldFrom(rule.Key)
			if ff != fields.FieldFromAttributes {
				continue
			}

			fk := funcKey{
				PredicateKey: field.PredicateKey,
				Kind:         field.Kind,
				Key:          v,
			}
			switch rule.Op {
			case funcOr:
				funcs[fk] = FuncOr(fields.TrimAttributesPrefix(rule.Values...)...)
			case funcContact:
				if len(rule.Values) != 2 {
					continue
				}
				vs := fields.TrimAttributesPrefix(rule.Values...)
				funcs[fk] = FuncContact(vs[0], vs[1], ":") // TODO(mando): 后续可考虑连接符配置化
			}
		}
	}

	return &SpanFieldNormalizer{
		ch:    ch,
		funcs: funcs,
	}
}

func (sfn SpanFieldNormalizer) Normalize(span ptrace.Span) {
	spanKind := span.Kind().String()
	predicateKeys := sfn.ch.GetPredicateKeys(spanKind)
	if len(predicateKeys) == 0 {
		return
	}

	for _, pk := range predicateKeys {
		ff, _ := fields.DecodeFieldFrom(pk)
		switch ff {
		case fields.FieldFromAttributes:
			attrKeys := sfn.ch.GetAttributes(spanKind, pk)
			for _, key := range attrKeys {
				fk := funcKey{
					PredicateKey: pk,
					Kind:         spanKind,
					Key:          key,
				}
				// 如果 key 空值则跳过
				if fn, ok := sfn.funcs[fk]; ok {
					fn(span, key)
				}
			}
		}
	}
}
