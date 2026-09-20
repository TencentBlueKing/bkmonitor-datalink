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
	"crypto/sha256"
	"encoding/json"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// StrategyHandle is an immutable executable projection of one validated
// StrategyIR. Every input carries the full strategy snapshot, so correctness
// does not depend on a separately populated strategy cache.
type StrategyHandle struct {
	tenantID               string
	purpose                string
	strategyRef            contract.StrategyRef
	checkWindowUnitSeconds int
	triggerConfigs         []contract.TriggerConfig
	executionFingerprint   [sha256.Size]byte
}

func NewStrategyHandle(strategy *contract.TriggerStrategyIR) (*StrategyHandle, error) {
	if err := strategy.Validate(); err != nil {
		return nil, err
	}
	return newValidatedStrategyHandle(strategy), nil
}

func newValidatedStrategyHandle(strategy *contract.TriggerStrategyIR) *StrategyHandle {
	execution, err := json.Marshal(struct {
		CheckWindowUnitSeconds int                      `json:"check_window_unit_seconds"`
		TriggerConfigs         []contract.TriggerConfig `json:"trigger_configs"`
	}{
		CheckWindowUnitSeconds: strategy.CheckWindowUnitSeconds,
		TriggerConfigs:         strategy.TriggerConfigs,
	})
	if err != nil {
		panic("marshal validated trigger execution plan: " + err.Error())
	}
	return &StrategyHandle{
		tenantID:               strategy.TenantID,
		purpose:                strategy.Purpose,
		strategyRef:            strategy.StrategyRef,
		checkWindowUnitSeconds: strategy.CheckWindowUnitSeconds,
		triggerConfigs:         append([]contract.TriggerConfig(nil), strategy.TriggerConfigs...),
		executionFingerprint:   sha256.Sum256(execution),
	}
}

func (h *StrategyHandle) TriggerConfigs() []contract.TriggerConfig {
	if h == nil {
		return nil
	}
	return append([]contract.TriggerConfig(nil), h.triggerConfigs...)
}
