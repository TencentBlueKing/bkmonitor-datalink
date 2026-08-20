// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trigger

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/contract"
)

func TestStrategyHandleDoesNotExposeMutableStrategyState(t *testing.T) {
	t.Parallel()

	strategy := newStrategy(t, "generation-1", []contract.TriggerConfig{{Level: 1, CheckWindowSize: 3, TriggerCount: 2}})
	handle, err := NewStrategyHandle(strategy)
	if err != nil {
		t.Fatalf("NewStrategyHandle() error = %v", err)
	}

	strategy.TriggerConfigs[0].TriggerCount = 99
	configs := handle.TriggerConfigs()
	configs[0].TriggerCount = 88

	if got := handle.TriggerConfigs()[0].TriggerCount; got != 2 {
		t.Fatalf("handle trigger count = %d, want 2", got)
	}
}
