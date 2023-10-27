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
	"fmt"
	"strings"
)

type PluginRunMode int

const (
	PluginRunModePush PluginRunMode = iota
	PluginRunModePull
	PluginRunModeUnknown
)
const GlobalBussinessId = "0"

type Plugin struct {
	PluginID   string      `json:"plugin_id"`
	PluginType string      `json:"plugin_type"`
	BusinessID interface{} `json:"bk_biz_id"`
}

func (p *Plugin) GetRunMode() PluginRunMode {
	if strings.HasSuffix(p.PluginType, "_push") {
		return PluginRunModePush
	}
	if strings.HasSuffix(p.PluginType, "_pull") {
		return PluginRunModePull
	}
	return PluginRunModeUnknown
}

func (p *Plugin) IsGlobalPlugin() bool {
	if p.BusinessID == nil {
		// 当业务ID为0的时候或者不存在的时候，表示全局
		return true
	}
	BusinessID := fmt.Sprintf("%v", p.BusinessID)
	return BusinessID == GlobalBussinessId
}
