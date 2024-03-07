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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var tempDir = ""

func getTempDirPattern(exe string) string {
	elems := append([]string{exe}, os.Args...)
	text := strings.Join(elems, "|")
	return fmt.Sprintf("%s-%x-*", filepath.Base(exe), md5.Sum([]byte(text)))
}

func removeTempDir(pattern string) error {
	names, err := filepath.Glob(filepath.Join(os.TempDir(), pattern))
	if err != nil {
		return err
	}
	for _, name := range names {
		err = os.RemoveAll(name)
		if err != nil {
			return err
		}
	}
	return nil
}

// InitTempDir initialize temp dir
func InitTempDir(clear bool) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("get exec failed %w", err)
	}
	dirPattern := getTempDirPattern(exe)
	if clear {
		err = removeTempDir(dirPattern)
		if err != nil {
			return "", fmt.Errorf("remove temp dir failed %s %v", dirPattern, err)
		}
	}
	dir, err := os.MkdirTemp("", dirPattern)
	if err == nil {
		tempDir = dir
	}
	return dir, err
}

// HasTempDir if temp dir exists
func HasTempDir() bool {
	return tempDir != ""
}

// CreateTempFile get temp file by pattern
func CreateTempFile(pattern string) (*os.File, error) {
	if tempDir == "" {
		return nil, errors.New("temp dir not init, call InitTempDir first")
	}

	// First determine whether the tempDir exists. If it does not exist, create it
	_, err := os.Stat(tempDir)
	if os.IsNotExist(err) {
		err = os.MkdirAll(tempDir, 0o755)
		if err != nil {
			return nil, errors.New("tempDir create fail, err:" + err.Error())
		}
	}

	return os.CreateTemp(tempDir, pattern)
}

// ClearTempFile clear temp file by pattern
func ClearTempFile(pattern string) error {
	names, err := filepath.Glob(filepath.Join(tempDir, pattern))
	if err != nil {
		return err
	}
	for _, name := range names {
		err = os.Remove(name)
		if err != nil {
			return err
		}
	}
	return nil
}
