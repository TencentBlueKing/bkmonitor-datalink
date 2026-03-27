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
	"encoding/gob"
	"fmt"
)

// HashIt : hash an object
// 使用 gob 编码对象并计算 SHA1 hash
// 注意：使用此函数前，需要确保相关类型已在各自的包中通过 gob.Register 注册
// 作用：将任意 Go 对象转换为 SHA1 哈希字符串，用于快速比较对象内容是否相同
func HashIt(object any) string {
	var (
		buf     bytes.Buffer
		encoder = gob.NewEncoder(&buf)
	)

	err := encoder.Encode(object)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", sha1.Sum(buf.Bytes()))
}

// init 注册基础类型到 gob
func init() {
	// 类型在gob中未注册，显示注册
	gob.Register([]any{})
	gob.Register(map[string]any{})
}
