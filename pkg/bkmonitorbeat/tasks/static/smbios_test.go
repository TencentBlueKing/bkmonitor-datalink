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
	"testing"
)

func smbiosFixture(major, minor byte, table []byte) []byte {
	raw := []byte{0, major, minor, 0, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(raw[4:], uint32(len(table)))
	return append(raw, table...)
}

func systemInfoFixture() []byte {
	// The UUID bytes are the example from the SMBIOS specification:
	// 00112233-4455-6677-8899-aabbccddeeff in SMBIOS 2.6 and newer.
	return append([]byte{1, 24, 0, 0, 0, 1, 0, 2,
		0x33, 0x22, 0x11, 0x00, 0x55, 0x44, 0x77, 0x66,
		0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		[]byte(" Example VM \r\nignored\x00 serial-1 \x00\x00")...)
}

func TestParseSMBIOSHardware(t *testing.T) {
	for _, tc := range []struct {
		name         string
		major, minor byte
		want         string
	}{
		{"2.6", 2, 6, "00112233-4455-6677-8899-aabbccddeeff"},
		{"3.0", 3, 0, "00112233-4455-6677-8899-aabbccddeeff"},
		{"2.5", 2, 5, "33221100-5544-7766-8899-aabbccddeeff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			table := append([]byte{0, 4, 0, 0, 0, 0}, systemInfoFixture()...)
			h := parseSMBIOSHardware(smbiosFixture(tc.major, tc.minor, table))
			want := Hardware{tc.want, "Example VM", "serial-1", uuidStatusOK}
			if *h != want {
				t.Fatalf("got %+v, want %+v", h, want)
			}
		})
	}
}

func TestParseSMBIOSMissingUUID(t *testing.T) {
	short := append([]byte{1, 8, 0, 0, 0, 1, 0, 2}, []byte("Example VM\x00serial-1\x00\x00")...)
	h := parseSMBIOSHardware(smbiosFixture(2, 6, short))
	if h.UUIDStatus != uuidStatusEmpty || h.ProductUUID != "" || h.ProductName != "Example VM" || h.ProductSerial != "serial-1" {
		t.Fatalf("got %+v", h)
	}
	for _, fill := range []byte{0, 0xff} {
		table := systemInfoFixture()
		copy(table[8:24], bytes.Repeat([]byte{fill}, 16))
		if h := parseSMBIOSHardware(smbiosFixture(2, 6, table)); h.UUIDStatus != uuidStatusEmpty || h.ProductUUID != "" {
			t.Fatalf("fill %x: %+v", fill, h)
		}
	}
}

func TestParseSMBIOSBounds(t *testing.T) {
	valid := smbiosFixture(2, 6, systemInfoFixture())
	for n := 0; n < len(valid); n++ {
		h := parseSMBIOSHardware(valid[:n])
		if h.UUIDStatus != uuidStatusUnreadable {
			t.Fatalf("truncated at %d: %+v", n, h)
		}
	}
	for _, table := range [][]byte{{1}, {1, 3, 0, 0, 0, 0}, {1, 24, 0, 0}, {1, 4, 0, 0, 'x', 0}} {
		if h := parseSMBIOSHardware(smbiosFixture(2, 6, table)); h.UUIDStatus != uuidStatusUnreadable {
			t.Fatalf("invalid %x: %+v", table, h)
		}
	}
	for _, table := range [][]byte{nil, {0, 4, 0, 0, 0, 0}, append([]byte{127, 4, 0, 0, 0, 0}, systemInfoFixture()...)} {
		if h := parseSMBIOSHardware(smbiosFixture(2, 6, table)); h.UUIDStatus != uuidStatusEmpty {
			t.Fatalf("missing Type 1: %+v", h)
		}
	}
	// Bytes beyond the declared table length must not be parsed.
	raw := append(smbiosFixture(2, 6, nil), systemInfoFixture()...)
	if h := parseSMBIOSHardware(raw); h.UUIDStatus != uuidStatusEmpty {
		t.Fatalf("ignored length: %+v", h)
	}
	for _, index := range []byte{0, 3, 255} {
		table := systemInfoFixture()
		table[5], table[7] = index, index
		if h := parseSMBIOSHardware(smbiosFixture(2, 6, table)); h.ProductName != "" || h.ProductSerial != "" || h.UUIDStatus != uuidStatusOK {
			t.Fatalf("index %d: %+v", index, h)
		}
	}
}
