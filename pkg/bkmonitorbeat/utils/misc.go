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
	"bytes"
	"crypto/sha1"
	"fmt"
	"os"
	"runtime"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

// PathExist : judge path exist or not
func PathExist(path string) bool {
	_, err := os.Stat(path)
	if err != nil && os.IsNotExist(err) {
		return false
	}
	return true
}

// GeneratorHashKey
func GeneratorHashKey(names []string) string {
	const keySepatator = "||"
	hash := sha1.New()

	var key bytes.Buffer
	for _, name := range names {
		key.WriteString(name)
		key.WriteString(keySepatator)
	}
	hash.Write(key.Bytes())
	return fmt.Sprintf("%x", hash.Sum(nil))
}

// RecoverFor : callback for recover
func RecoverFor(fn func(error)) {
	err := recover()
	if err != nil {
		fn(err.(error))
	}
}

// CleanCompositeParamList 清洗符合接口定义格式的对象,是CleanCompositableConfigs的优化版
func CleanCompositeParamList(configs ...define.CompositeParam) error {
	for _, conf := range configs {
		err := conf.CleanParams()
		if err != nil {
			return err
		}
	}
	return nil
}

func PidStoreFile() string {
	pidstore := ".pidstore"
	// windows 对 dotfile 支持不友好
	if runtime.GOOS == "windows" {
		return "pidstore"
	}
	return pidstore
}
