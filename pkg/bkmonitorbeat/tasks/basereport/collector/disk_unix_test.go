// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build linux
// +build linux

package collector

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIOCountersWithContext(t *testing.T) {
	//create a temporary file for testing
	tempFile, err := os.CreateTemp("", "diskstats")
	assert.NoError(t, err)
	defer os.RemoveAll(tempFile.Name())

	//  write  some  contents  to  the  file
	contents := []byte("      8              0  sda  1  2  3  4  5  6  7  8  9  10  11  12")
	_, err = tempFile.Write(contents)
	assert.NoError(t, err)

	//  call  the  function  with  the  temporary  file  as  argument
	stats, err := IOCountersWithContext(context.Background(), tempFile.Name())
	assert.NoError(t, err)

	//  verify  the  returned  value
	expectedStats := map[string]BKDiskStats{"sda": {ReadCount: 1, WriteCount: 3, ReadBytes: 5, WriteBytes: 7, ReadTime: 9, WriteTime: 11, IoTime: 12}}
	assert.True(t, equalMaps(stats, expectedStats))
}

// equalMaps  compares  two  maps  for  equality
func equalMaps(a, b map[string]BKDiskStats) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			return false
		}
		if !reflect.DeepEqual(va, vb) {
			return false
		}
	}
	return true
}
