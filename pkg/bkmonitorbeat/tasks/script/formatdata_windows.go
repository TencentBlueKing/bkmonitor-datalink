// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package script

import (
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func ShellWordPreProcess(cmdline string) string {
	cmdline = strings.Replace(cmdline, "\\", "\\\\", -1)
	target := cmdline
	if strings.Count(cmdline, "%") >= 2 && strings.Count(cmdline, "%")%2 == 0 {
		envList := strings.Split(cmdline, "%")
		firstIndex := strings.Index(cmdline, "%")
		found := false
		for i, env := range envList {
			if env == "" {
				continue
			}
			if env == " " || env == "  " {
				found = true
				continue
			}
			if firstIndex == 0 {
				target = "$" + env
				continue
			}
			if i == 0 {
				target = env
				continue
			}
			if found {
				target = target + " $" + env
				found = false
			} else {
				target = target + "$" + env
			}
		}
	}
	logger.Infof("cmdline:%s, after format:%s", cmdline, target)
	return target
}
