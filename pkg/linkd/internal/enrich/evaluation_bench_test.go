// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"linkd/internal/domain"
)

// BenchmarkEvaluationChain 比较固定来源载荷下多步骤丰富的编解码和分配成本。
func BenchmarkEvaluationChain(b *testing.B) {
	for _, steps := range []int{1, 8} {
		b.Run(fmt.Sprintf("steps_%d", steps), func(b *testing.B) {
			event := testAlert()
			event.SourceRawData["payload"] = json.RawMessage(`{"message":"` + strings.Repeat("source-data", 4096) + `"}`)
			processors := make([]Processor, steps)
			for i := range processors {
				processors[i] = &testProcessor{name: fmt.Sprintf("step_%d", i), fn: func(context.Context, *Scope) (ProcessorResult, error) {
					patch, err := domain.NewEnrichPatch("$.content", fmt.Sprintf("step %d", i))
					return ProcessorResult{Status: domain.EnrichStatusSucceeded, Patches: []domain.EnrichPatch{patch}}, err
				}}
			}
			chain, err := NewChain(processors, Sources{})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := chain.Enrich(b.Context(), Input{Event: event}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
