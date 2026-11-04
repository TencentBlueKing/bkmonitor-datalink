// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build basetask

package taskfactory

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

// TestBaseTaskBuildRegistersRequiredFactories guards the full Beat build from
// silently omitting task factories that can still be parsed from the main and
// child configuration files.
func TestBaseTaskBuildRegistersRequiredFactories(t *testing.T) {
	required := []string{
		define.ModuleStatic,
		define.ModuleBasereport,
		define.ModuleExceptionbeat,
		define.ModuleProcConf,
		define.ModuleActiveProfile,
	}

	for _, taskType := range required {
		if _, ok := mappings[taskType]; !ok {
			t.Errorf("task factory %q is not registered in the basetask build", taskType)
		}
	}
}
