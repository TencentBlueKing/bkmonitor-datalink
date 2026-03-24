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
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// SplitTraces returns one ptrace.Traces for each trace in the given ptrace.Traces input. Each of the resulting ptrace.Traces contains exactly one trace.
func SplitTraces(batch ptrace.Traces) []ptrace.Traces {
	// for each span in the resource spans, we group them into batches of rs/ils/traceID.
	// if the same traceID exists in different ils, they land in different batches.
	var result []ptrace.Traces

	for i := 0; i < batch.ResourceSpans().Len(); i++ {
		rs := batch.ResourceSpans().At(i)

		for j := 0; j < rs.ScopeSpans().Len(); j++ {
			// the batches for this ILS
			batches := map[pcommon.TraceID]ptrace.ResourceSpans{}

			ils := rs.ScopeSpans().At(j)
			for k := 0; k < ils.Spans().Len(); k++ {
				span := ils.Spans().At(k)
				key := span.TraceID()

				// for the first traceID in the ILS, initialize the map entry
				// and add the singleTraceBatch to the result list
				if _, ok := batches[key]; !ok {
					trace := ptrace.NewTraces()
					newRS := trace.ResourceSpans().AppendEmpty()
					// currently, the ResourceSpans implementation has only a Resource and an ILS. We'll copy the Resource
					// and set our own ILS
					rs.Resource().CopyTo(newRS.Resource())
					newRS.SetSchemaUrl(rs.SchemaUrl())
					newILS := newRS.ScopeSpans().AppendEmpty()
					// currently, the ILS implementation has only an InstrumentationLibrary and spans. We'll copy the library
					// and set our own spans
					ils.Scope().CopyTo(newILS.Scope())
					newILS.SetSchemaUrl(ils.SchemaUrl())
					batches[key] = newRS

					result = append(result, trace)
				}

				// there is only one instrumentation library per batch
				tgt := batches[key].ScopeSpans().At(0).Spans().AppendEmpty()
				span.CopyTo(tgt)
			}
		}
	}

	return result
}

func SplitEachSpans(batch ptrace.Traces) []ptrace.Traces {
	var result []ptrace.Traces

	for i := 0; i < batch.ResourceSpans().Len(); i++ {
		rs := batch.ResourceSpans().At(i)

		for j := 0; j < rs.ScopeSpans().Len(); j++ {
			ils := rs.ScopeSpans().At(j)
			for k := 0; k < ils.Spans().Len(); k++ {
				span := ils.Spans().At(k)
				trace := ptrace.NewTraces()

				newRS := trace.ResourceSpans().AppendEmpty()
				rs.Resource().CopyTo(newRS.Resource())
				newRS.SetSchemaUrl(rs.SchemaUrl())
				newILS := newRS.ScopeSpans().AppendEmpty()

				ils.Scope().CopyTo(newILS.Scope())
				newILS.SetSchemaUrl(ils.SchemaUrl())
				spans := newRS.ScopeSpans().At(0).Spans().AppendEmpty()
				span.CopyTo(spans)

				result = append(result, trace)
			}
		}
	}

	return result
}
