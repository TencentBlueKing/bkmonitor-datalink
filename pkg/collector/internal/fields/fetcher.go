// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fields

import (
	"strconv"

	"go.opentelemetry.io/collector/pdata/ptrace"
)

type SpanFieldFetcher struct{}

func NewSpanFieldFetcher() SpanFieldFetcher {
	return SpanFieldFetcher{}
}

func (sff SpanFieldFetcher) FetchResource(resourceSpans ptrace.ResourceSpans, key string) string {
	rs := resourceSpans.Resource().Attributes()
	if v, ok := rs.Get(key); ok {
		return v.AsString()
	}
	return ""
}

func (sff SpanFieldFetcher) FetchResourcesTo(resourceSpans ptrace.ResourceSpans, keys []string, dst map[string]string) map[string]string {
	rs := resourceSpans.Resource().Attributes()
	for _, key := range keys {
		if v, ok := rs.Get(key); ok {
			dst[key] = v.AsString()
		}
	}
	return dst
}

func (sff SpanFieldFetcher) FetchAttribute(span ptrace.Span, key string) string {
	v, ok := span.Attributes().Get(key)
	if !ok {
		return ""
	}
	return v.AsString()
}

func (sff SpanFieldFetcher) FetchAttributesTo(span ptrace.Span, keys []string, dst map[string]string) {
	attrs := span.Attributes()
	for _, key := range keys {
		if v, ok := attrs.Get(key); ok {
			dst[key] = v.AsString()
		}
	}
}

func (sff SpanFieldFetcher) FetchMethod(span ptrace.Span, key string) string {
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

func (sff SpanFieldFetcher) FetchMethodsTo(span ptrace.Span, keys []string, dst map[string]string) {
	for _, key := range keys {
		dst[key] = sff.FetchMethod(span, key)
	}
}
