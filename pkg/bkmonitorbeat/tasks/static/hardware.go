// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific language governing permissions and limitations under the License.

package static

import (
	"os"
	"runtime"
	"strings"
)

const (
	uuidStatusOK          = "ok"
	uuidStatusEmpty       = "empty"
	uuidStatusUnreadable  = "unreadable"
	uuidStatusUnsupported = "unsupported"

	dmiProductUUIDPath   = "/sys/class/dmi/id/product_uuid"
	dmiProductNamePath   = "/sys/class/dmi/id/product_name"
	dmiProductSerialPath = "/sys/class/dmi/id/product_serial"
)

// Hardware 是主机 DMI 硬件身份。ProductUUID 只在 UUIDStatus 为 ok 时有值。
type Hardware struct {
	ProductUUID   string
	ProductName   string
	ProductSerial string
	UUIDStatus    string
}

var readDMIFile = os.ReadFile

func getHardware() *Hardware {
	if runtime.GOOS != "linux" {
		return &Hardware{UUIDStatus: uuidStatusUnsupported}
	}
	return collectLinuxHardware()
}

func collectLinuxHardware() *Hardware {
	uuidRaw, uuidErr := readDMIValue(dmiProductUUIDPath)
	name, nameErr := readDMIValue(dmiProductNamePath)
	serial, serialErr := readDMIValue(dmiProductSerialPath)

	productUUID, status := classifyProductUUID(uuidRaw, uuidErr)
	hardware := &Hardware{
		ProductUUID: productUUID,
		UUIDStatus:  status,
	}
	if nameErr == nil {
		hardware.ProductName = name
	}
	if serialErr == nil {
		hardware.ProductSerial = serial
	}
	return hardware
}

func readDMIValue(path string) (string, error) {
	data, err := readDMIFile(path)
	if err != nil {
		return "", err
	}
	text := strings.ReplaceAll(string(data), "\x00", "")
	line := text
	if index := strings.IndexAny(text, "\r\n"); index >= 0 {
		line = text[:index]
	}
	return strings.TrimSpace(line), nil
}

func classifyProductUUID(raw string, err error) (string, string) {
	if err != nil {
		if os.IsNotExist(err) {
			return "", uuidStatusEmpty
		}
		return "", uuidStatusUnreadable
	}

	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" || value == "none" || value == "not specified" || isAllZeroUUID(value) {
		return "", uuidStatusEmpty
	}
	return value, uuidStatusOK
}

func isAllZeroUUID(value string) bool {
	compact := strings.ReplaceAll(value, "-", "")
	if compact == "" {
		return false
	}
	for _, char := range compact {
		if char != '0' {
			return false
		}
	}
	return true
}
