// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func TestParseRequestIP(t *testing.T) {
	t.Run("localhost", func(t *testing.T) {
		assert.Equal(t, "", ParseRequestIP("localhost", nil))
	})

	t.Run("127.0.0.1", func(t *testing.T) {
		assert.Equal(t, "127.0.0.1", ParseRequestIP("127.0.0.1", nil))
	})

	t.Run("127.0.0.1:8080", func(t *testing.T) {
		assert.Equal(t, "127.0.0.1", ParseRequestIP("127.0.0.1:8080", nil))
	})
}

func TestGetGrpcIpFromContext(t *testing.T) {
	assert.Zero(t, GetGrpcIpFromContext(context.Background()))
}

func TestIsValidFloat64(t *testing.T) {
	assert.True(t, IsValidFloat64(1.0))
	assert.False(t, IsValidFloat64(math.NaN()))
}

func TestIsValidUint64(t *testing.T) {
	assert.True(t, IsValidUint64(1))
	assert.False(t, IsValidUint64(uint64(math.NaN())))
}

func TestCalcSpanDuration(t *testing.T) {
	t.Run("Abnormal", func(t *testing.T) {
		span := ptrace.NewSpan()
		span.SetStartTimestamp(pcommon.Timestamp(20000))
		span.SetEndTimestamp(pcommon.Timestamp(10000))
		assert.Equal(t, float64(0), CalcSpanDuration(span))
	})

	t.Run("Ok", func(t *testing.T) {
		span := ptrace.NewSpan()
		span.SetStartTimestamp(pcommon.Timestamp(10000))
		span.SetEndTimestamp(pcommon.Timestamp(20000))
		assert.Equal(t, float64(10000), CalcSpanDuration(span))
	})
}

func TestFirstUpper(t *testing.T) {
	tests := []struct {
		input  string
		output string
	}{
		{
			input:  "foo",
			output: "Foo",
		},
		{
			input:  "FOO",
			output: "Foo",
		},
		{
			input:  "",
			output: "x",
		},
	}

	for _, c := range tests {
		assert.Equal(t, c.output, FirstUpper(c.input, "x"))
	}
}
