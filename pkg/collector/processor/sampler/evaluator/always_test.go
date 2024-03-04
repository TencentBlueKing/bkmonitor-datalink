// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
)

func TestAlwaysEvaluator(t *testing.T) {
	evaluator := New(Config{})

	g := generator.NewTracesGenerator(define.TracesOptions{
		SpanCount: 10,
	})

	traces := g.Generate()
	record := &define.Record{
		RecordType: define.RecordTraces,
		Data:       traces,
	}
	assert.Equal(t, 10, record.Data.(ptrace.Traces).SpanCount())
	assert.NoError(t, evaluator.Evaluate(record))
	assert.Equal(t, 10, record.Data.(ptrace.Traces).SpanCount())
}
