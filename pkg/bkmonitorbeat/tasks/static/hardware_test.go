// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific language governing permissions and limitations under the License.

package static

import (
	"errors"
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyProductUUID(t *testing.T) {
	uuid, status := classifyProductUUID("ABCdefab-1234-5678-9abc-def012345678", nil)
	assert.Equal(t, "abcdefab-1234-5678-9abc-def012345678", uuid)
	assert.Equal(t, uuidStatusOK, status)

	for _, raw := range []string{"", "  ", "None", "not specified", "00000000-0000-0000-0000-000000000000"} {
		uuid, status = classifyProductUUID(raw, nil)
		assert.Empty(t, uuid, raw)
		assert.Equal(t, uuidStatusEmpty, status, raw)
	}

	uuid, status = classifyProductUUID("", os.ErrNotExist)
	assert.Empty(t, uuid)
	assert.Equal(t, uuidStatusEmpty, status)

	uuid, status = classifyProductUUID("", os.ErrPermission)
	assert.Empty(t, uuid)
	assert.Equal(t, uuidStatusUnreadable, status)
}

func TestCollectLinuxHardware(t *testing.T) {
	original := readDMIFile
	t.Cleanup(func() { readDMIFile = original })

	files := map[string]struct {
		data []byte
		err  error
	}{
		dmiProductUUIDPath:   {data: []byte("ABCDEFAB-1234-5678-9ABC-DEF012345678\n")},
		dmiProductNamePath:   {data: []byte("KVM\n")},
		dmiProductSerialPath: {data: []byte("serial-1\n")},
	}
	readDMIFile = func(path string) ([]byte, error) {
		item, ok := files[path]
		if !ok {
			return nil, os.ErrNotExist
		}
		return item.data, item.err
	}

	hardware := collectLinuxHardware()
	assert.Equal(t, "abcdefab-1234-5678-9abc-def012345678", hardware.ProductUUID)
	assert.Equal(t, uuidStatusOK, hardware.UUIDStatus)
	assert.Equal(t, "KVM", hardware.ProductName)
	assert.Equal(t, "serial-1", hardware.ProductSerial)

	files[dmiProductUUIDPath] = struct {
		data []byte
		err  error
	}{err: os.ErrPermission}
	hardware = collectLinuxHardware()
	assert.Empty(t, hardware.ProductUUID)
	assert.Equal(t, uuidStatusUnreadable, hardware.UUIDStatus)
	assert.Equal(t, "KVM", hardware.ProductName)
	assert.Equal(t, "serial-1", hardware.ProductSerial)

	files[dmiProductNamePath] = struct {
		data []byte
		err  error
	}{err: errors.New("read name failed")}
	hardware = collectLinuxHardware()
	assert.Empty(t, hardware.ProductName)
	assert.Equal(t, "serial-1", hardware.ProductSerial)
}

func TestGetHardwareOnNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux collects DMI files")
	}
	hardware := getHardware()
	assert.Equal(t, uuidStatusUnsupported, hardware.UUIDStatus)
	assert.Empty(t, hardware.ProductUUID)
	assert.Empty(t, hardware.ProductName)
	assert.Empty(t, hardware.ProductSerial)
}
