// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package process

import (
	"strings"
	"testing"

	"linkd/internal/config"
)

func TestValidateConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		config    config.Config
		wantError string
	}{
		{name: "no enabled sources"},
		{
			name: "enabled source without storage",
			config: config.Config{EventSources: []config.EventSource{{
				EventSourceID: "source-1",
				Enabled:       true,
			}}},
			wantError: "storage config is required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateConfig(test.config)
			if test.wantError == "" && err != nil {
				t.Fatalf("ValidateConfig() error = %v", err)
			}
			if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("ValidateConfig() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}
