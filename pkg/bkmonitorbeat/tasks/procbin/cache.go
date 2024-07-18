// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package procbin

import (
	"crypto/md5"
	"fmt"
	"os"
	"sync"
)

var (
	hashCacheMut sync.Mutex
	hashCache    = map[pidCreated]string{}
)

type pidCreated struct {
	pid     int32
	created int64
}

func hashWithCached(pc pidCreated, p string) string {
	hashCacheMut.Lock()
	defer hashCacheMut.Unlock()

	h, ok := hashCache[pc]
	if ok {
		return h
	}

	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	h = fmt.Sprintf("%x", md5.Sum(b))
	hashCache[pc] = h
	return h
}

func cleanupCached(pcs map[pidCreated]struct{}) {
	hashCacheMut.Lock()
	defer hashCacheMut.Unlock()

	for k := range hashCache {
		_, ok := pcs[k]
		if !ok {
			// 如果一个进程在原先存在 但在新的集合中不存在 则证明进程已经销毁
			delete(hashCache, k)
		}
	}
}
