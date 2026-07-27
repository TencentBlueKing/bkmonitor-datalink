// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package v1alpha1

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
)

func TestBkLogConfigIsMatchBkEnv(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		bkEnvs   []string
		expected bool
	}{
		{
			name:     "missing label matches empty environment",
			bkEnvs:   []string{""},
			expected: true,
		},
		{
			name:     "empty label matches empty environment",
			labels:   map[string]string{config.BkEnvLabelName: ""},
			bkEnvs:   []string{""},
			expected: true,
		},
		{
			name:     "primary environment matches",
			labels:   map[string]string{config.BkEnvLabelName: "bkop"},
			bkEnvs:   []string{"bkop", ""},
			expected: true,
		},
		{
			name:     "additional environment matches",
			labels:   map[string]string{config.BkEnvLabelName: "prod"},
			bkEnvs:   []string{"bkop", "prod"},
			expected: true,
		},
		{
			name:   "other environment does not match",
			labels: map[string]string{config.BkEnvLabelName: "stag"},
			bkEnvs: []string{"bkop", "prod"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bkLogConfig := &BkLogConfig{ObjectMeta: metav1.ObjectMeta{Labels: tt.labels}}
			if actual := bkLogConfig.IsMatchBkEnv(tt.bkEnvs); actual != tt.expected {
				t.Fatalf("unexpected match result: got %t, want %t", actual, tt.expected)
			}
		})
	}
}
