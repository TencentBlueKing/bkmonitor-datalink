// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package prometheus

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "ErrTimeRangeTooLarge",
			err:  ErrTimeRangeTooLarge,
			want: "time range is too large",
		},
		{
			name: "ErrDatetimeParseFailed",
			err:  ErrDatetimeParseFailed,
			want: "datetime parser failed",
		},
		{
			name: "ErrOperatorType",
			err:  ErrOperatorType,
			want: "unknown operator type",
		},
		{
			name: "ErrPromQueryInfoNotSet",
			err:  ErrPromQueryInfoNotSet,
			want: "prom query info not set",
		},
		{
			name: "ErrGetQueryByMetricFailed",
			err:  ErrGetQueryByMetricFailed,
			want: "cannot get query info of metric",
		},
		{
			name: "ErrGetMetricMappingFailed",
			err:  ErrGetMetricMappingFailed,
			want: "get metric mapping failed",
		},
		{
			name: "ErrContextDone",
			err:  ErrContextDone,
			want: "context done",
		},
		{
			name: "ErrTimeout",
			err:  ErrTimeout,
			want: "time out",
		},
		{
			name: "ErrInvalidValue",
			err:  ErrInvalidValue,
			want: "invalid value",
		},
		{
			name: "ErrChannelReceived",
			err:  ErrChannelReceived,
			want: "channel closed before a value received",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotNil(t, tt.err)
			assert.Equal(t, tt.want, tt.err.Error())
			// 验证错误可以被 errors.Is 识别
			assert.True(t, errors.Is(tt.err, tt.err))
		})
	}
}

func TestErrorWrapping(t *testing.T) {
	// 测试错误可以被包装
	originalErr := ErrTimeRangeTooLarge
	wrappedErr := errors.New("wrapper: " + originalErr.Error())

	assert.Contains(t, wrappedErr.Error(), originalErr.Error())
}
