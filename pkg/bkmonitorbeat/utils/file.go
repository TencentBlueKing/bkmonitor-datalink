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
	"os"
)

func ReadFileTail(p string, size int64) ([]byte, error) {
	if size <= 0 {
		return nil, nil
	}

	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}

	fileSize := info.Size()
	if fileSize < size {
		size = fileSize
	}

	buffer := make([]byte, size)
	file, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	_, err = file.Seek(-size, 2)
	if err != nil {
		return nil, err
	}

	n, err := file.Read(buffer)
	if err != nil {
		return nil, err
	}

	return buffer[:n], nil
}
