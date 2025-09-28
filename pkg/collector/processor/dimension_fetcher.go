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
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/alias"
)

type DimensionFromType uint8

const (
	DimensionFromUnknown DimensionFromType = iota
	DimensionFromResource
	DimensionFromAttribute
	DimensionFromMethod
)

func DecodeDimensionFrom(s string) (DimensionFromType, string) {
	switch {
	case len(s) == 0:
		return DimensionFromUnknown, s
	case strings.HasPrefix(s, define.ResourceKeyPrefix):
		return DimensionFromResource, s[len(define.ResourceKeyPrefix):]
	case strings.HasPrefix(s, define.AttributeKeyPrefix):
		return DimensionFromAttribute, s[len(define.AttributeKeyPrefix):]
	default:
		return DimensionFromMethod, s
	}
}

func NewSpanDimensionFetcher() SpanDimensionFetcher {
	return SpanDimensionFetcher{}
}

type SpanDimensionFetcher struct{}

func (sdf SpanDimensionFetcher) FetchResource(resourceSpans ptrace.ResourceSpans, key string) string {
	attrs := resourceSpans.Resource().Attributes()
	if v, ok := attrs.Get(key); ok {
		return v.AsString()
	}
	return ""
}

func (sdf SpanDimensionFetcher) FetchResources(resourceSpans ptrace.ResourceSpans, keys ...string) map[string]string {
	attrs := resourceSpans.Resource().Attributes()
	dimensions := make(map[string]string)
	for _, key := range keys {
		if v, ok := attrs.Get(key); ok {
			dimensions[key] = v.AsString()
		}
	}
	return dimensions
}

func (sdf SpanDimensionFetcher) FetchAttribute(span ptrace.Span, key string) string {
	v, _ := alias.GetAttributes(span, key)
	return v
}

func (sdf SpanDimensionFetcher) FetchAttributes(span ptrace.Span, dimensions map[string]string, keys []string) {
	for _, key := range keys {
		if v, ok := alias.GetAttributes(span, key); ok {
			dimensions[key] = v
		}
	}
}

func (sdf SpanDimensionFetcher) FetchMethod(span ptrace.Span, key string) string {
	switch key {
	case "span_id":
		return span.SpanID().HexString()
	case "span_name":
		return span.Name()
	case "trace_id":
		return span.TraceID().HexString()
	case "kind":
		return strconv.Itoa(int(span.Kind()))
	case "status.code":
		return strconv.Itoa(int(span.Status().Code()))
	}
	return ""
}

func (sdf SpanDimensionFetcher) FetchMethods(span ptrace.Span, dimensions map[string]string, keys []string) {
	for _, key := range keys {
		dimensions[key] = sdf.FetchMethod(span, key)
	}
}
