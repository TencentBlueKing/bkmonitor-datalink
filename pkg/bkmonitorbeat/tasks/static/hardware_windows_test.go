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
	"testing"
)

func TestCollectWindowsHardware(t *testing.T) {
	original := readRawSMBIOS
	t.Cleanup(func() { readRawSMBIOS = original })
	for _, tc := range []struct {
		err    error
		status string
	}{
		{errSMBIOSUnsupported, uuidStatusUnsupported},
		{errors.New("read failed"), uuidStatusUnreadable},
		{nil, uuidStatusOK},
	} {
		readRawSMBIOS = func() ([]byte, error) { return smbiosFixture(2, 6, systemInfoFixture()), tc.err }
		h := getHardware()
		if h.UUIDStatus != tc.status {
			t.Fatalf("got %+v, want %s", h, tc.status)
		}
		if tc.err == nil && h.ProductUUID != "00112233-4455-6677-8899-aabbccddeeff" {
			t.Fatalf("got %+v", h)
		}
		if tc.err != nil && h.ProductUUID != "" {
			t.Fatalf("got %+v", h)
		}
	}
}
