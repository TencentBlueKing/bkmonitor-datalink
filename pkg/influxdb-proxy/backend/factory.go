// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package backend

import (
	"context"
)

// NewBackendFunc Backend生成方法，生成一个指定类型的Backend
type NewBackendFunc func(ctx context.Context, config *BasicConfig) (Backend, chan *Status, error)

// 存储所有生成方法
var backendFactory map[string]NewBackendFunc

func init() {
	backendFactory = make(map[string]NewBackendFunc)
}

// RegisterBackend 注册指定类型的backend
func RegisterBackend(name string, backendFunc NewBackendFunc) {
	backendFactory[name] = backendFunc
}

// GetBackendFunc 获取指定类型的backend
func GetBackendFunc(name string) NewBackendFunc {
	return backendFactory[name]
}
