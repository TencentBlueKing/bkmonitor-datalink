// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package optmap

import (
	"strings"

	"github.com/spf13/cast"
)

func NameOpts(s string) (string, string) {
	if s == "" {
		return "", ""
	}

	nameOpts := strings.Split(s, ";")
	if len(nameOpts) == 1 {
		return nameOpts[0], ""
	}
	return nameOpts[0], nameOpts[1]
}

type OptMap struct {
	m map[string]any // 不会有并发读写
}

func New(s string) *OptMap {
	m := make(map[string]any)
	pairs := strings.Split(s, ",")
	for _, pair := range pairs {
		kv := strings.Split(strings.TrimSpace(pair), "=")
		if len(kv) != 2 {
			continue
		}
		m[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	}
	return &OptMap{m: m}
}

func (om *OptMap) GetInt(k string) (int, bool) {
	v, ok := om.m[k]
	if !ok {
		return 0, false
	}

	i, err := cast.ToIntE(v)
	if err != nil {
		return 0, false
	}
	return i, true
}

func (om *OptMap) GetIntDefault(k string, defaultVal int) int {
	i, ok := om.GetInt(k)
	if ok {
		return i
	}
	return defaultVal
}
