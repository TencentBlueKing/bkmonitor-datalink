// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cleaner

import (
	"sync"
)

type CleanFunc func() error

var (
	mut        sync.Mutex
	cleanFuncs = map[string]CleanFunc{}
)

func Register(name string, fn CleanFunc) {
	mut.Lock()
	defer mut.Unlock()

	cleanFuncs[name] = fn
}

func CleanFuncs() map[string]CleanFunc {
	mut.Lock()
	defer mut.Unlock()

	ret := make(map[string]CleanFunc)
	for name, fn := range cleanFuncs {
		ret[name] = fn
	}
	return ret
}
