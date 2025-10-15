// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestShortDur(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{
			name:     "1 second",
			duration: time.Second,
			expected: "1s",
		},
		{
			name:     "1 minute",
			duration: time.Minute,
			expected: "1m",
		},
		{
			name:     "1 hour",
			duration: time.Hour,
			expected: "1h",
		},
		{
			name:     "1 hour 30 minutes",
			duration: time.Hour + 30*time.Minute,
			expected: "90m",
		},
		{
			name:     "1 hour 0 minutes",
			duration: time.Hour + 0*time.Minute,
			expected: "1h",
		},
		{
			name:     "1 minute 0 seconds",
			duration: time.Minute + 0*time.Second,
			expected: "1m",
		},
		{
			name:     "1 hour 30 minutes 0 seconds",
			duration: time.Hour + 30*time.Minute + 0*time.Second,
			expected: "90m",
		},
		{
			name:     "1 hour 0 minutes 0 seconds",
			duration: time.Hour + 0*time.Minute + 0*time.Second,
			expected: "1h",
		},
		{
			name:     "1 minute 30 seconds",
			duration: time.Minute + 30*time.Second,
			expected: "90s",
		},
		{
			name:     "0 seconds",
			duration: 0 * time.Second,
			expected: "0ms",
		},
		{
			name:     "1 day",
			duration: 24 * time.Hour,
			expected: "1d",
		},
		{
			name:     "1 day 0 hours",
			duration: 24*time.Hour + 0*time.Hour,
			expected: "1d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shortDur(tt.duration)
			assert.Equal(t, tt.expected, got)
		})
	}
}
