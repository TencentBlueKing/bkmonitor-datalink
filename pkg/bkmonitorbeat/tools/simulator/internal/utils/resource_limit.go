// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"crypto/md5"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
)

func getResourceIdentify(name string) string {
	path, err := os.Executable()
	if err != nil {
		path = os.Args[0]
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return name
	}
	name = strings.ReplaceAll(name, "-", "")
	return fmt.Sprintf("%s_%x", name, md5.Sum([]byte(path)))
}

// SetResourceLimit 限制当前进程资源
func SetResourceLimit(name string, cpu float64, mem int64) {
	if mem > 0 {
		debug.SetMemoryLimit(mem)
	}
	name = getResourceIdentify(name)
	err := setResourceLimit(name, cpu, mem)
	if err != nil {
		log.Println("setResourceLimit failed:", err)
		// CPU 核数向上取整 确保有核可用
		// 0.1 -> 1 core
		if cpu > 0 {
			runtime.GOMAXPROCS(int(math.Ceil(cpu)))
		}
		return
	}
	// 如果 cgroup 限制设置成功 则允许进程在所有核心上进行调度
	runtime.GOMAXPROCS(0)
}

var rMap sync.Map

var deleteFunc = func(r interface{}) error { return nil }

func storeRMap(key string, r interface{}) {
	rMap.Store(key, r)
}

// DeleteResourceLimit 删除限制资源对象
func DeleteResourceLimit(name string) error {
	if r, ok := rMap.Load(getResourceIdentify(name)); ok {
		return deleteFunc(r)
	}
	return nil
}
