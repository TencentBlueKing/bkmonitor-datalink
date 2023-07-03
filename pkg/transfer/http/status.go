// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"net/http"
	"os"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// ProcessView return process status
func ProcessView(writer http.ResponseWriter, request *http.Request) {
	WriteJSONResponse(http.StatusOK, writer, map[string]interface{}{
		"pid":        os.Getpid(),
		"ppid":       os.Getppid(),
		"uid":        os.Getuid(),
		"gid":        os.Getgid(),
		"euid":       os.Geteuid(),
		"egid":       os.Getegid(),
		"client_id":  define.ProcessID,
		"version":    define.Version,
		"build_hash": define.BuildHash,
		"mode":       define.Mode,
	})
}

// SettingsView return all settings
func SettingsView(writer http.ResponseWriter, request *http.Request) {
	WriteJSONResponse(http.StatusOK, writer, config.Configuration.AllSettings())
}

func init() {
	http.HandleFunc("/status/process", ProcessView)
	http.HandleFunc("/status/settings", SettingsView)
}
