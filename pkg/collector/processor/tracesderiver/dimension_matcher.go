// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tracesderiver

import (
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

// DimensionMatcher 负责匹配 span 维度
type DimensionMatcher interface {
	Types() []TypeWithName
	ResourceKeys(t string) []string
	MatchResource(resourceSpans ptrace.ResourceSpans) map[string]string
	Match(t string, ptrace ptrace.Span) (map[string]string, bool)
}

func NewSpanDimensionMatcher(ch *ConfigHandler) DimensionMatcher {
	return spanDimensionMatcher{
		ch:      ch,
		fetcher: processor.NewSpanDimensionFetcher(),
	}
}

type spanDimensionMatcher struct {
	ch      *ConfigHandler
	fetcher processor.SpanDimensionFetcher
}

// Match 一种 type 只能提取一个指标
// type: 黄金指标 duration...
// predicateKeys: 一个黄金指标可能会存在多种 SPAN_KIND 如耗时可能是 rpc/http/db 的调用耗时 但一个 span 只可能属于其中一种 举例：
// - attributes.db.system
// - attributes.rpc.system
// - attributes.http.method
func (sdm spanDimensionMatcher) Match(t string, span ptrace.Span) (map[string]string, bool) {
	spanKind := span.Kind().String()
	predicateKeys := sdm.ch.GetPredicateKeys(t, spanKind)
	if len(predicateKeys) == 0 {
		return nil, false
	}

	dimensions := make(map[string]string)
	var found bool
loop:
	for _, pk := range predicateKeys {
		df, k := processor.DecodeDimensionFrom(pk)
		switch df {
		// TODO(mando): 目前 predicateKey 暂时只支持 attributes 后续可能会扩展
		case processor.DimensionFromAttribute:
			// 如果 key 空值则跳过
			if s := sdm.fetcher.FetchAttribute(span, k); s == "" {
				continue
			}

			found = true
			sdm.fetcher.FetchAttributes(span, dimensions, sdm.ch.GetAttributes(t, spanKind, pk))
			sdm.fetcher.FetchMethods(span, dimensions, sdm.ch.GetMethods(t, spanKind, pk))
			break loop

		case processor.DimensionFromUnknown:
			// 退避处理
			found = true
			sdm.fetcher.FetchAttributes(span, dimensions, sdm.ch.GetAttributes(t, spanKind, pk))
			sdm.fetcher.FetchMethods(span, dimensions, sdm.ch.GetMethods(t, spanKind, pk))
			break loop
		}
	}

	if found {
		return dimensions, true
	}
	return nil, false
}

func (sdm spanDimensionMatcher) Types() []TypeWithName {
	return sdm.ch.GetTypes()
}

func (sdm spanDimensionMatcher) ResourceKeys(t string) []string {
	return sdm.ch.GetResourceKeys(t)
}

func (sdm spanDimensionMatcher) MatchResource(resourceSpans ptrace.ResourceSpans) map[string]string {
	ret := make(map[string]string)
	types := sdm.ch.GetTypes()
	for i := 0; i < len(types); i++ {
		m := sdm.fetcher.FetchResources(resourceSpans, sdm.ch.GetResourceKeys(types[i].Type))
		for k, v := range m {
			ret[k] = v
		}
	}
	return ret
}
