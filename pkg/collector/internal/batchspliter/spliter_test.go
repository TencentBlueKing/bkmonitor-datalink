// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//
// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package batchspliter

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
)

func TestSplitDifferentTracesIntoDifferentBatches(t *testing.T) {
	// we have 1 ResourceSpans with 1 ILS and two traceIDs, resulting in two batches
	inBatch := ptrace.NewTraces()
	rs := inBatch.ResourceSpans().AppendEmpty()
	rs.SetSchemaUrl("https://opentelemetry.io/schemas/1.6.1")
	// the first ILS has two spans
	ils := rs.ScopeSpans().AppendEmpty()
	ils.SetSchemaUrl("https://opentelemetry.io/schemas/1.6.1")
	library := ils.Scope()
	library.SetName("first-library")
	firstSpan := ils.Spans().AppendEmpty()
	firstSpan.SetName("first-batch-first-span")

	firstSpan.SetTraceID(pcommon.NewTraceID([16]byte{1, 2, 3, 4}))
	secondSpan := ils.Spans().AppendEmpty()
	secondSpan.SetName("first-batch-second-span")
	secondSpan.SetTraceID(pcommon.NewTraceID([16]byte{2, 3, 4, 5}))

	// test
	out := SplitTraces(inBatch)

	// verify
	assert.Len(t, out, 2)

	// first batch
	firstOutRS := out[0].ResourceSpans().At(0)
	assert.Equal(t, rs.SchemaUrl(), firstOutRS.SchemaUrl())

	firstOutILS := out[0].ResourceSpans().At(0).ScopeSpans().At(0)
	assert.Equal(t, library.Name(), firstOutILS.Scope().Name())
	assert.Equal(t, firstSpan.Name(), firstOutILS.Spans().At(0).Name())
	assert.Equal(t, ils.SchemaUrl(), firstOutILS.SchemaUrl())

	// second batch
	secondOutRS := out[1].ResourceSpans().At(0)
	assert.Equal(t, rs.SchemaUrl(), secondOutRS.SchemaUrl())

	secondOutILS := out[1].ResourceSpans().At(0).ScopeSpans().At(0)
	assert.Equal(t, library.Name(), secondOutILS.Scope().Name())
	assert.Equal(t, secondSpan.Name(), secondOutILS.Spans().At(0).Name())
	assert.Equal(t, ils.SchemaUrl(), secondOutILS.SchemaUrl())
}

func TestSplitTracesWithNilTraceID(t *testing.T) {
	// prepare
	inBatch := ptrace.NewTraces()
	rs := inBatch.ResourceSpans().AppendEmpty()
	rs.SetSchemaUrl("https://opentelemetry.io/schemas/1.6.1")
	ils := rs.ScopeSpans().AppendEmpty()
	ils.SetSchemaUrl("https://opentelemetry.io/schemas/1.6.1")
	firstSpan := ils.Spans().AppendEmpty()
	firstSpan.SetTraceID(pcommon.NewTraceID([16]byte{}))

	// test
	batches := SplitTraces(inBatch)

	// verify
	assert.Len(t, batches, 1)
	assert.Equal(t, pcommon.NewTraceID([16]byte{}), batches[0].ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).TraceID())
	assert.Equal(t, rs.SchemaUrl(), batches[0].ResourceSpans().At(0).SchemaUrl())
	assert.Equal(t, ils.SchemaUrl(), batches[0].ResourceSpans().At(0).ScopeSpans().At(0).SchemaUrl())
}

func TestSplitSameTraceIntoDifferentBatches(t *testing.T) {
	// prepare
	inBatch := ptrace.NewTraces()
	rs := inBatch.ResourceSpans().AppendEmpty()
	rs.SetSchemaUrl("https://opentelemetry.io/schemas/1.6.1")

	// we have 1 ResourceSpans with 2 ILS, resulting in two batches
	rs.ScopeSpans().EnsureCapacity(2)

	// the first ILS has two spans
	firstILS := rs.ScopeSpans().AppendEmpty()
	firstILS.SetSchemaUrl("https://opentelemetry.io/schemas/1.6.1")

	firstLibrary := firstILS.Scope()
	firstLibrary.SetName("first-library")
	firstILS.Spans().EnsureCapacity(2)
	firstSpan := firstILS.Spans().AppendEmpty()
	firstSpan.SetName("first-batch-first-span")
	firstSpan.SetTraceID(pcommon.NewTraceID([16]byte{1, 2, 3, 4}))
	secondSpan := firstILS.Spans().AppendEmpty()
	secondSpan.SetName("first-batch-second-span")
	secondSpan.SetTraceID(pcommon.NewTraceID([16]byte{1, 2, 3, 4}))

	// the second ILS has one span
	secondILS := rs.ScopeSpans().AppendEmpty()
	secondILS.SetSchemaUrl("https://opentelemetry.io/schemas/1.6.1")

	secondLibrary := secondILS.Scope()
	secondLibrary.SetName("second-library")
	thirdSpan := secondILS.Spans().AppendEmpty()
	thirdSpan.SetName("second-batch-first-span")
	thirdSpan.SetTraceID(pcommon.NewTraceID([16]byte{1, 2, 3, 4}))

	// test
	batches := SplitTraces(inBatch)

	// verify
	assert.Len(t, batches, 2)

	// first batch
	assert.Equal(t, rs.SchemaUrl(), batches[0].ResourceSpans().At(0).SchemaUrl())
	assert.Equal(t, firstILS.SchemaUrl(), batches[0].ResourceSpans().At(0).ScopeSpans().At(0).SchemaUrl())
	assert.Equal(t, pcommon.NewTraceID([16]byte{1, 2, 3, 4}), batches[0].ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).TraceID())
	assert.Equal(t, firstLibrary.Name(), batches[0].ResourceSpans().At(0).ScopeSpans().At(0).Scope().Name())
	assert.Equal(t, firstSpan.Name(), batches[0].ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Name())
	assert.Equal(t, secondSpan.Name(), batches[0].ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(1).Name())

	// second batch
	assert.Equal(t, rs.SchemaUrl(), batches[1].ResourceSpans().At(0).SchemaUrl())
	assert.Equal(t, secondILS.SchemaUrl(), batches[1].ResourceSpans().At(0).ScopeSpans().At(0).SchemaUrl())
	assert.Equal(t, pcommon.NewTraceID([16]byte{1, 2, 3, 4}), batches[1].ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).TraceID())
	assert.Equal(t, secondLibrary.Name(), batches[1].ResourceSpans().At(0).ScopeSpans().At(0).Scope().Name())
	assert.Equal(t, thirdSpan.Name(), batches[1].ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Name())
}

func TestSplitEachSpans(t *testing.T) {
	t1 := random.TraceID()
	t2 := random.TraceID()

	traces := ptrace.NewTraces()
	rs := traces.ResourceSpans().AppendEmpty()
	span1 := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span1.SetTraceID(t1)
	span1.Status().SetCode(ptrace.StatusCodeError)

	span2 := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span2.SetTraceID(t1)
	span2.Status().SetCode(ptrace.StatusCodeOk)

	span3 := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span3.SetTraceID(t2)
	span3.Status().SetCode(ptrace.StatusCodeUnset)

	result := SplitEachSpans(traces)
	assert.Len(t, result, 3)

	statusCode := []ptrace.StatusCode{
		ptrace.StatusCodeError,
		ptrace.StatusCodeOk,
		ptrace.StatusCodeUnset,
	}

	n := 0
	for i := 0; i < len(statusCode); i++ {
		foreach.Spans(result[i].ResourceSpans(), func(span ptrace.Span) {
			assert.Equal(t, statusCode[n], span.Status().Code())
			n++
		})
	}
}

func TestSplitTracesWithJson(t *testing.T) {
	b, err := os.ReadFile("../../example/fixtures/traces1.json")
	assert.NoError(t, err)
	traces, err := generator.FromJsonToTraces(b)
	assert.NoError(t, err)
	assert.Equal(t, 15, traces.SpanCount())

	items := SplitTraces(traces)
	assert.Len(t, items, 4)
}

func TestSplitEachSpansWithJson(t *testing.T) {
	b, err := os.ReadFile("../../example/fixtures/traces1.json")
	assert.NoError(t, err)
	traces, err := generator.FromJsonToTraces(b)
	assert.NoError(t, err)
	assert.Equal(t, 15, traces.SpanCount())

	items := SplitEachSpans(traces)
	assert.Len(t, items, 15)
}
