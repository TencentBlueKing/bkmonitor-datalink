// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build !integration
// +build !integration

package file

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsSameFile(t *testing.T) {

	// 创建测试的文件
	absPath, err := filepath.Abs(".")

	var (
		testPath   = filepath.Join(absPath, "test.log")
		systemPath = filepath.Join(absPath, "system.log")
	)
	_, _ = os.Create(testPath)
	_, _ = os.Create(systemPath)

	assert.NotNil(t, absPath)
	assert.Nil(t, err)

	fileInfo1, err := os.Stat(testPath)
	fileInfo2, err := os.Stat(systemPath)

	assert.Nil(t, err)
	assert.NotNil(t, fileInfo1)
	assert.NotNil(t, fileInfo2)

	file1 := &File{
		State: State{
			FileInfo: fileInfo1,
		},
	}

	file2 := &File{
		State: State{
			FileInfo: fileInfo2,
		},
	}

	file3 := &File{
		State: State{
			FileInfo: fileInfo2,
		},
	}

	assert.False(t, file1.IsSameFile(file2))
	assert.False(t, file2.IsSameFile(file1))

	assert.True(t, file1.IsSameFile(file1))
	assert.True(t, file2.IsSameFile(file2))

	assert.True(t, file3.IsSameFile(file2))
	assert.True(t, file2.IsSameFile(file3))

	_ = os.Remove(testPath)
	_ = os.Remove(systemPath)
}
