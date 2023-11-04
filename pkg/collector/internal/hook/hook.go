// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package hook

import (
	"context"
	"os/exec"
	"runtime"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var hookTriggerTotal = prometheus.NewCounter(
	prometheus.CounterOpts{
		Namespace: define.MonitoringNamespace,
		Name:      "hook_trigger_total",
		Help:      "Hook trigger total",
	},
)

func init() {
	prometheus.MustRegister(hookTriggerTotal)
}

var mgr Manager

func Register(c Config) {
	cfg := &c
	cfg.Validate()
	mgr = Manager{cfg: *cfg}
}

func OnFailureHook() {
	mgr.OnFailureHook()
}

type Config struct {
	OnFailure OnFailureConfig `config:"on_failure"`
}

func (c *Config) Validate() {
	if c.OnFailure.Timeout <= 0 {
		c.OnFailure.Timeout = time.Minute
	}
}

type OnFailureConfig struct {
	Timeout time.Duration `config:"timeout"`
	Scripts []string      `config:"scripts"`
}

// Manager

type Manager struct {
	cfg Config
}

func (m Manager) OnFailureHook() {
	hookTriggerTotal.Add(1)
	se := NewScriptExecutor(m.cfg.OnFailure.Scripts, m.cfg.OnFailure.Timeout)
	for _, ret := range se.Execute() {
		logger.Infof("execute command=[%s], err=%v", ret.Script, ret.Err)
	}
}

// ScriptExecutor

type ScriptResult struct {
	Script string
	Err    error
}

type ScriptExecutor struct {
	timeout time.Duration
	scripts []string
}

func NewScriptExecutor(scripts []string, timeout time.Duration) *ScriptExecutor {
	return &ScriptExecutor{
		timeout: timeout,
		scripts: scripts,
	}
}

func (s *ScriptExecutor) Execute() []ScriptResult {
	rets := make([]ScriptResult, 0)
	for _, script := range s.scripts {
		rets = append(rets, s.execute(script))
	}
	return rets
}

func (s *ScriptExecutor) execute(script string) ScriptResult {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(ctx, "cmd", "/C", script)
	default:
		cmd = exec.CommandContext(ctx, "bash", "-c", script)
	}
	return ScriptResult{
		Script: script,
		Err:    cmd.Run(),
	}
}
