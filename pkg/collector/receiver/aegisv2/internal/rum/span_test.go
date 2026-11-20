// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package rum

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func testSpan(typ Type, name string) ptrace.Span {
	return AppendSpan(ptrace.NewSpanSlice(), typ, name,
		pcommon.TraceID([16]byte{1}), pcommon.SpanID([8]byte{2}), pcommon.Timestamp(1000))
}

func TestAppendSpanContract(t *testing.T) {
	for _, tc := range []struct {
		typ  Type
		name string
		want string
		kind ptrace.SpanKind
	}{
		{Session, "", "browser.session", ptrace.SpanKindInternal},
		{View, "", "browser.view", ptrace.SpanKindInternal},
		{Resource, "", "browser.resource", ptrace.SpanKindClient},
		{Action, "action.checkout", "action.checkout", ptrace.SpanKindInternal},
		{Error, "", "browser.error", ptrace.SpanKindInternal},
		{Error, "browser.unhandledrejection", "browser.unhandledrejection", ptrace.SpanKindInternal},
		{Error, "browser.resource_error", "browser.resource_error", ptrace.SpanKindInternal},
		{Websocket, "", "browser.websocket", ptrace.SpanKindClient},
		{Vital, "", "browser.web_vital", ptrace.SpanKindInternal},
		{Custom, "custom.checkout", "custom.checkout", ptrace.SpanKindInternal},
	} {
		t.Run(tc.want, func(t *testing.T) {
			span := testSpan(tc.typ, tc.name)
			assert.Equal(t, tc.want, span.Name())
			assert.Equal(t, tc.kind, span.Kind())
			assert.Equal(t, span.StartTimestamp(), span.EndTimestamp())
			assert.Equal(t, pcommon.Timestamp(1000), span.StartTimestamp())
			assert.Equal(t, pcommon.TraceID([16]byte{1}), span.TraceID())
			assert.Equal(t, pcommon.SpanID([8]byte{2}), span.SpanID())
			assert.Equal(t, ptrace.StatusCodeUnset, span.Status().Code())
			if tc.typ == Vital {
				span.Attributes().PutString("vital.metric", "cls")
				span.Attributes().PutDouble("vital.value", 0)
				span.Attributes().PutString("vital.id", "vital-1")
			}
			ApplySpanDefaults(span)
			require.NoError(t, ValidateSpan(span))
		})
	}
}

func TestValidateSpanRejectsBrokenContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(ptrace.Span)
	}{
		{"unknown type", func(s ptrace.Span) { s.Attributes().PutString(SpanTypeKey, "unknown") }},
		{"missing type", func(s ptrace.Span) { s.Attributes().Remove(SpanTypeKey) }},
		{"wrong name", func(s ptrace.Span) { s.SetName("browser.view") }},
		{"wrong kind", func(s ptrace.Span) { s.SetKind(ptrace.SpanKindInternal) }},
		{"missing trace ID", func(s ptrace.Span) { s.SetTraceID(pcommon.TraceID{}) }},
		{"missing span ID", func(s ptrace.Span) { s.SetSpanID(pcommon.SpanID{}) }},
		{"negative duration", func(s ptrace.Span) { s.SetEndTimestamp(999) }},
		{"invalid outcome", func(s ptrace.Span) { s.Attributes().PutString(OutcomeTypeKey, "ok") }},
		{"wrong outcome type", func(s ptrace.Span) { s.Attributes().PutBool(OutcomeTypeKey, false) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			span := testSpan(Resource, "")
			tc.change(span)
			ApplySpanDefaults(span)
			require.Error(t, ValidateSpan(span))
		})
	}
	for _, typ := range []Type{Action, Custom} {
		span := testSpan(typ, string(typ)+".")
		ApplySpanDefaults(span)
		require.Error(t, ValidateSpan(span))
	}
}
