// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"time"
)

type BalanceConfig struct {
	RecorderEnabled    bool    `json:"recorder_enabled"`
	AutoBalanceEnabled bool    `json:"autobalance_enabled"`
	Fluctuation        float64 `json:"fluctuation"`
	ForceRound         int     `json:"force_round"`
	CheckInterval      string  `json:"check_interval"`
	LogPath            string  `json:"log_path"`
}

func (bc BalanceConfig) CheckIntervalDuration() time.Duration {
	dur, err := time.ParseDuration(bc.CheckInterval)
	if err != nil {
		return time.Minute * 2
	}
	return dur
}

var DefaultBalanceConfig = BalanceConfig{
	RecorderEnabled:    false,
	AutoBalanceEnabled: false,
	Fluctuation:        0.3,
	CheckInterval:      "2m",
	LogPath:            "",
}

// BaseScheduler : base scheduler
type BaseScheduler struct{}

// Start :
func (s *BaseScheduler) Start() error {
	return nil
}

// Stop :
func (s *BaseScheduler) Stop() error {
	return nil
}

// Wait :
func (s *BaseScheduler) Wait() error {
	return nil
}

// NewBaseScheduler :
func NewBaseScheduler() *BaseScheduler {
	return &BaseScheduler{}
}
