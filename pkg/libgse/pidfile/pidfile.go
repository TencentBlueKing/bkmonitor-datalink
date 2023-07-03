// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pidfile

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/nightlyone/lockfile"

	bkcommon "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
)

var lock lockfile.Lockfile

// GetPid get pid from pidfile
func GetPid(pidFilePath string) (int, error) {
	// read file
	buf, err := ioutil.ReadFile(pidFilePath)
	if err != nil {
		return -1, err
	}
	pid := bkcommon.ScanPidLine(buf)
	if pid <= 0 {
		return -1, fmt.Errorf("can not get pid!")
	}
	return int(pid), err
}

// TryLock try to create lockfile
func TryLock(pidFilePath string) error {
	// ensure pid path exist
	dir := filepath.Dir(pidFilePath)
	err := os.MkdirAll(dir, 0o775)
	if err != nil {
		fmt.Printf("Cannot create pid directory. reason: %v", err)
		return err
	}

	lock, err = lockfile.New(pidFilePath)
	if err != nil {
		fmt.Printf("Cannot init lock. reason: %v", err)
		return err
	}
	err = lock.TryLock()
	// Error handling is essential, as we only try to get the lock.
	if err != nil {
		fmt.Printf("Cannot lock %q, reason: %v", lock, err)
		return err
	}
	return nil
}

func UnLock() {
	lock.Unlock()
}
