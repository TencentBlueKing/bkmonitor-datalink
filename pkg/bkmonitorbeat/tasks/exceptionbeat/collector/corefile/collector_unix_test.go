// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package corefile

import (
	"context"
	"os"
	"path"
	"testing"
	"time"

	"github.com/magiconair/properties/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/exceptionbeat/collector"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
)

var (
	messageCount    = 0
	coreFilePath    = path.Join(os.TempDir(), "corefile")
	corePatternPath = path.Join(os.TempDir(), "corefile_pattern")
	coreUsesPidPath = path.Join(os.TempDir(), "core_uses_pid")
)

func newMock() {
	collector.Send = func(dataid int, extra beat.MapStr, e chan<- define.Event) {
		messageCount++
	}

	err := os.WriteFile(corePatternPath, []byte(path.Join(coreFilePath, "%e.corefile\n")), 0o644)
	if err != nil {
		panic(err)
	}
	err = os.WriteFile(coreUsesPidPath, []byte("0"), 0o644)
	if err != nil {
		panic(err)
	}
	err = os.MkdirAll(coreFilePath, 0o755)
	if err != nil {
		panic(err)
	}

	CorePatternFile = corePatternPath
	CoreUsesPidFile = coreUsesPidPath
}

func resetMock() {
	messageCount = 0

	_ = os.Remove(corePatternPath)
	_ = os.RemoveAll(coreFilePath)
}

func newTestConfig() *configs.ExceptionBeatConfig {
	defaultConfig := configs.DefaultExceptionBeatConfig
	defaultConfig.CheckBit = configs.Core

	return &defaultConfig
}

func TestCorefileCreate(t *testing.T) {
	newMock()
	defer resetMock()

	c := new(CoreFileCollector)
	c.state = closeState
	e := make(chan define.Event)
	c.Start(context.Background(), e, newTestConfig())

	time.Sleep(1 * time.Millisecond)
	// 创建一个新的corefile文件，需要有新的计数
	_, err := os.Create(path.Join(coreFilePath, "haha.corefile"))
	if err != nil {
		panic(err)
	}
	time.Sleep(1 * time.Second)

	assert.Equal(t, messageCount, 1)
}
