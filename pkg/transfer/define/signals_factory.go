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
	"github.com/pkg/errors"
)

// mapSignals : Payload factory mappings
var mapSignals = make(map[string]string)

// RegisterSignalName : register Payload to factory
var RegisterSignalName = func(name, value string) {
	if name == "" {
		panic(errors.New("name can not be empty"))
	}
	mapSignals[name] = value
}

// UnregisterSignalName
var UnregisterSignalName = func(name string) (string, bool) {
	layout, ok := mapSignals[name]
	if !ok {
		return "", false
	}
	delete(mapSignals, name)
	return layout, true
}

// GetSignalByName
var GetSignalByName = func(name string) (string, bool) {
	value, ok := mapSignals[name]
	if !ok {
		return "", false
	}
	return value, true
}

// ListSignalNames
var ListSignalNames = func() []string {
	keys := make([]string, 0, len(mapSignals))
	for key := range mapSignals {
		keys = append(keys, key)
	}
	return keys
}

func init() {
	RegisterPlugin(&PluginInfo{
		Name:       "SignalName",
		Registered: ListSignalNames,
	})
}
