// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import "testing"

func TestAlarmdResourceConfiguration(t *testing.T) {
	endpoint := "http://alarmd-control:8081/internal/v1/no-data-closures"
	path := writeConfig(t, "resources:\n  alarmd:\n    no_data_close_endpoint: "+endpoint+"\n")
	cfg, err := Load(path, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	resource := cfg.Resources.Alarmd.WithDefaults()
	if resource.EventSourceID != "built_in_bk" || resource.TimeoutMilliseconds != 2000 || resource.NoDataCloseEndpoint != endpoint {
		t.Fatalf("configuration = %+v", resource)
	}
	clone := cfg.Resources.Redacted()
	clone.Alarmd.EventSourceID = "other"
	if cfg.Resources.Alarmd.EventSourceID == "other" {
		t.Fatal("configuration clone shares alarmd resource")
	}
	for _, tc := range []struct {
		name     string
		resource AlarmdResource
		invalid  bool
	}{
		{"empty", AlarmdResource{}, false},
		{"disabled ignores other fields", AlarmdResource{TimeoutMilliseconds: -1}, false},
		{"custom source", AlarmdResource{NoDataCloseEndpoint: endpoint, EventSourceID: "alarmd", TimeoutMilliseconds: 1000}, false},
		{"bad scheme", AlarmdResource{NoDataCloseEndpoint: "file:///tmp/alarmd"}, true},
		{"credentials", AlarmdResource{NoDataCloseEndpoint: "http://user:secret@alarmd/internal"}, true}, // #nosec G101 -- 仅用于验证拒绝 URL 凭据的固定测试样例。
		{"query", AlarmdResource{NoDataCloseEndpoint: endpoint + "?token=x"}, true},
		{"invalid timeout", AlarmdResource{NoDataCloseEndpoint: endpoint, TimeoutMilliseconds: -1}, true},
		{"excess timeout", AlarmdResource{NoDataCloseEndpoint: endpoint, TimeoutMilliseconds: 10001}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := (ResourcesConfig{Alarmd: &tc.resource}).Validate(); (err != nil) != tc.invalid {
				t.Fatalf("validation = %v", err)
			}
		})
	}
}
