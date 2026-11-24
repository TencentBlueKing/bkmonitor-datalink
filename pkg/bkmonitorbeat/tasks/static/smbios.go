// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific language governing permissions and limitations under the License.

package static

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// parseSMBIOSHardware reads the RawSMBIOSData returned by GetSystemFirmwareTable.
// UUID byte order follows Linux dmi_save_uuid: SMBIOS 2.6 and newer use little
// endian for the first three UUID fields; earlier versions use network order.
func parseSMBIOSHardware(raw []byte) *Hardware {
	unreadable := func() *Hardware { return &Hardware{UUIDStatus: uuidStatusUnreadable} }
	if len(raw) < 8 {
		return unreadable()
	}
	length := uint64(binary.LittleEndian.Uint32(raw[4:8]))
	if length > uint64(len(raw)-8) {
		return unreadable()
	}
	table := raw[8 : 8+int(length)]
	for len(table) > 0 {
		if len(table) < 4 || table[1] < 4 || int(table[1]) > len(table) {
			return unreadable()
		}
		size := int(table[1])
		end := bytes.Index(table[size:], []byte{0, 0})
		if end < 0 {
			return unreadable()
		}
		if table[0] == 127 {
			break
		}
		if table[0] == 1 {
			hardware := &Hardware{UUIDStatus: uuidStatusEmpty}
			strings := bytes.Split(table[size:size+end], []byte{0})
			stringAt := func(offset int) string {
				if offset >= size || table[offset] == 0 || int(table[offset]) > len(strings) {
					return ""
				}
				return cleanHardwareString(string(strings[int(table[offset])-1]))
			}
			hardware.ProductName = stringAt(5)
			hardware.ProductSerial = stringAt(7)
			if size >= 0x18 {
				uuid := table[8:24]
				if !bytes.Equal(uuid, bytes.Repeat([]byte{0}, 16)) && !bytes.Equal(uuid, bytes.Repeat([]byte{0xff}, 16)) {
					hardware.ProductUUID, hardware.UUIDStatus = classifyProductUUID(formatSMBIOSUUID(uuid, raw[1], raw[2]), nil)
				}
			}
			return hardware
		}
		table = table[size+end+2:]
	}
	return &Hardware{UUIDStatus: uuidStatusEmpty}
}

func formatSMBIOSUUID(b []byte, major, minor byte) string {
	if major > 2 || (major == 2 && minor >= 6) {
		return fmt.Sprintf("%08x-%04x-%04x-%x-%x", binary.LittleEndian.Uint32(b[:4]), binary.LittleEndian.Uint16(b[4:6]), binary.LittleEndian.Uint16(b[6:8]), b[8:10], b[10:16])
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
