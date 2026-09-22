// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"linkd/internal/config"
	"linkd/internal/runtimeconfig"
)

func TestDisabledDynamicConfigDoesNotConstructStore(t *testing.T) {
	for _, cp := range []*config.ControlPlaneConfig{nil, {}, {DynamicConfig: &config.DynamicConfigConfig{}}, {DynamicConfig: &config.DynamicConfigConfig{Sources: map[string]config.DynamicSourceConfig{"offline": {Type: config.DynamicSourceAlarmLevel}}}}} {
		cfg := config.Config{ControlPlane: cp}
		state := runtimeconfig.NewSeverity(config.DefaultSeverityConfig())
		manager, closeAll, err := openDynamicConfig(context.Background(), cfg, state, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil || manager != nil {
			t.Fatalf("manager=%v err=%v", manager, err)
		}
		if err = closeAll(); err != nil {
			t.Fatal(err)
		}
		if err = prepareDynamicConfigSnapshots(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
	}
}
