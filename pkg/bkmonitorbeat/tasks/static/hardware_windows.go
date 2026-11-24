// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific language governing permissions and limitations under the License.

//go:build windows

package static

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procGetSystemFirmwareTable = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemFirmwareTable")
	errSMBIOSUnsupported       = errors.New("GetSystemFirmwareTable unavailable")
	readRawSMBIOS              = readWindowsSMBIOS
)

func collectWindowsHardware() *Hardware {
	raw, err := readRawSMBIOS()
	if errors.Is(err, errSMBIOSUnsupported) {
		return &Hardware{UUIDStatus: uuidStatusUnsupported}
	}
	if err != nil {
		return &Hardware{UUIDStatus: uuidStatusUnreadable}
	}
	return parseSMBIOSHardware(raw)
}

func readWindowsSMBIOS() ([]byte, error) {
	if err := procGetSystemFirmwareTable.Find(); err != nil {
		return nil, errSMBIOSUnsupported
	}
	const rsmbSignature = 0x52534d42 // 'RSMB', table ID 0 selects the raw table.
	size, _, err := procGetSystemFirmwareTable.Call(rsmbSignature, 0, 0, 0)
	if size == 0 {
		return nil, fmt.Errorf("GetSystemFirmwareTable size query failed: %v", err)
	}
	buf := make([]byte, size)
	n, _, err := procGetSystemFirmwareTable.Call(rsmbSignature, 0, uintptr(unsafe.Pointer(&buf[0])), size)
	if n == 0 {
		return nil, fmt.Errorf("GetSystemFirmwareTable read failed: %v", err)
	}
	if n > size {
		return nil, fmt.Errorf("GetSystemFirmwareTable buffer too small: have %d, need %d", size, n)
	}
	return buf[:n], nil
}
