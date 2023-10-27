// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package reloader

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/elastic/beats/libbeat/common"
)

type MockProc struct{}

var isReload = false

func (*MockProc) Reload(_ *common.Config) {
	isReload = true
}

func Test_reload(t *testing.T) {
	p := &MockProc{}
	name := "bkdata_test"

	// create config file
	cfgFile := "beat.yml"
	os.Remove(cfgFile)
	f, err := os.OpenFile(cfgFile, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte(name + ":"))
	f.Close()
	defer os.Remove(cfgFile)

	// write pid file
	pidFilePath := "/tmp/bkdata_test" // windows will ignore the path
	os.Remove(pidFilePath)
	f, err = os.Create(pidFilePath)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte(strconv.Itoa(os.Getpid())))
	f.Close()

	reloader := NewReloader(name, p)
	if err := reloader.Run(pidFilePath); err != nil {
		t.Fatal(err)
	}
	defer reloader.Stop()
	time.Sleep(1 * time.Second)

	if err := ReloadEvent(name, pidFilePath); err != nil {
		t.Fatal(err)
	}

	time.Sleep(1 * time.Second)
	if !isReload {
		t.Fatal("reload failed")
	}
}
