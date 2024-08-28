// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package input

import (
	"os"

	"golang.org/x/sys/windows"
)

func getFileInode(filename string, _ os.FileInfo) (uint64, error) {
	name, err := windows.UTF16PtrFromString(filename)
	if err != nil {
		return 0, err
	}
	handle, err := windows.CreateFile(name,
		windows.GENERIC_READ,
		0,
		nil,
		windows.OPEN_EXISTING,
		0,
		0)
	if err != nil {
		return 0, err
	}
	defer func(handle windows.Handle) {
		_ = windows.CloseHandle(handle)
	}(handle)

	var data windows.ByHandleFileInformation

	if err = windows.GetFileInformationByHandle(handle, &data); err != nil {
		return 0, err
	}

	return (uint64(data.FileIndexHigh) << 32) | uint64(data.FileIndexLow), nil
}
