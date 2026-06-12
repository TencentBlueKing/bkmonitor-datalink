// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tenant

import "testing"

func TestParseGseMessageEndpointPort(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     uint
	}{
		{
			name:     "port only",
			endpoint: "26001",
			want:     26001,
		},
		{
			name:     "host port",
			endpoint: "127.0.0.1:26001",
			want:     26001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGseMessageEndpointPort(tt.endpoint)
			if err != nil {
				t.Fatalf("parseGseMessageEndpointPort(%q) returned error: %v", tt.endpoint, err)
			}
			if got != tt.want {
				t.Fatalf("parseGseMessageEndpointPort(%q) = %d, want %d", tt.endpoint, got, tt.want)
			}
		})
	}
}

func TestParseGseMessageEndpointPortInvalid(t *testing.T) {
	tests := []string{
		"",
		"0",
		"127.0.0.1:0",
		"abc",
		"127.0.0.1:abc",
		"10.0.0.1:26001",
	}

	for _, endpoint := range tests {
		t.Run(endpoint, func(t *testing.T) {
			if got, err := parseGseMessageEndpointPort(endpoint); err == nil {
				t.Fatalf("parseGseMessageEndpointPort(%q) = %d, want error", endpoint, got)
			}
		})
	}
}
