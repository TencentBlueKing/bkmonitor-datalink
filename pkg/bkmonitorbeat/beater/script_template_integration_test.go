// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build integration && (scripttask || basetask)

package beater_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/beater"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Run with python3 and Jinja2 on PATH to exercise the same renderer as NodeMan.
func TestScriptTemplateConfigLoad(t *testing.T) {
	const child = "BKMONITORBEAT_TEST_SCRIPT_CONFIG_CHILD"
	const secret = "config-load-test-secret"
	const debugProbe = "script config test debug output"
	if os.Getenv(child) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$")
		cmd.Env = append(os.Environ(), child+"=1")
		logs, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", logs)
		require.Contains(t, string(logs), debugProbe)
		require.NotContains(t, string(logs), secret)
		return
	}
	logger.SetLoggerLevel(logger.DebugLevelDesc)
	logger.Debug(debugProbe)
	engine := beater.NewBaseConfigEngine(context.Background()).(*beater.BaseConfigEngine)

	const render = `
import json, sys
from pathlib import Path
from jinja2.sandbox import SandboxedEnvironment
template = SandboxedEnvironment().from_string(Path(sys.argv[1]).read_text(encoding="utf-8"))
print(template.render(json.load(sys.stdin)))
`
	templates, err := filepath.Glob("../support-files/templates/*/*/etc/bkmonitorbeat_script.conf.tpl")
	require.NoError(t, err)
	require.NotEmpty(t, templates)
	for _, path := range templates {
		for name, env := range map[string]map[string]string{
			"omitted":    nil,
			"null":       nil,
			"empty":      {},
			"configured": {"PASSWORD": secret, "COUNT": "001", "EMPTY": ""},
			"quoted":     {"TEXT": "中文: # \"a\"\n", "PATH": `C:\tools\bin`},
		} {
			t.Run(path+"/"+name, func(t *testing.T) {
				context := map[string]any{
					"config_name": "test", "config_version": "1.0", "dataid": 1,
					"task_id": 2, "bk_biz_id": 3, "period": 60, "command": "test-script",
				}
				if name != "omitted" {
					context["user_env"] = env
				}
				input, err := json.Marshal(context)
				require.NoError(t, err)
				cmd := exec.Command("python3", "-c", render, path)
				cmd.Stdin = bytes.NewReader(input)
				cmd.Stderr = os.Stderr
				rendered, err := cmd.Output()
				require.NoError(t, err)

				dir := t.TempDir()
				configPath := filepath.Join(dir, "script.yaml")
				require.NoError(t, os.WriteFile(configPath, rendered, 0o600))
				files, err := os.ReadDir(dir)
				require.NoError(t, err)
				require.Len(t, files, 1)
				task, err := engine.GetChildTask(files[0], dir)
				require.NoError(t, err)
				require.NoError(t, task.Clean())
				meta := task.TaskMetaConfig.(*configs.ScriptTaskMetaConfig)
				require.Len(t, meta.Tasks, 1)
				if len(env) == 0 {
					require.Empty(t, meta.Tasks[0].UserEnvs)
				} else {
					require.Equal(t, env, meta.Tasks[0].UserEnvs)
				}
				if name == "configured" {
					// config_path loads the same tasks through a separate logging path.
					meta := configs.NewScriptTaskMetaConfig(configs.NewConfig())
					meta.TaskConfigPath = configPath
					require.NoError(t, meta.Clean())
					require.Len(t, meta.Tasks, 1)
					require.Equal(t, env, meta.Tasks[0].UserEnvs)
				}
			})
		}
	}
}
