// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package events

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/docker/docker/pkg/reexec"
)

const (
	EventCoreDump  = "coredump"
	EventOOM       = "oom"
	EventDiskSpace = "diskspace"
	EventDiskRO    = "diskro"
	EventKeyword   = "keyword"
)

var AllEventNames = []string{
	EventCoreDump,
	EventOOM,
	EventDiskSpace,
	EventDiskRO,
	EventKeyword,
}

func InitExceptionsReexec() {
	reexec.Register(EventCoreDump, MakeCoreDump)
	reexec.Register(EventOOM, MakeOOM)
	reexec.Register(EventDiskSpace, MakeDiskSpace)
	reexec.Register(EventDiskRO, MakeDiskRO)
	reexec.Register(EventKeyword, MakeKeyword)
}

type exceptFunc struct {
	name    string
	raise   func()
	recover func()
}

type ExceptConfig struct {
	DiskSpacePath string
	DiskROPath    string
	KeywordPath   string
}

var defaultExceptConfig = &ExceptConfig{}

func getRegexecFunc(name string, envs ...string) func() {
	return func() {
		defer func() {
			if r := recover(); r != nil {
				log.Println("panic recover:", r)
			}
		}()
		cmd := reexec.Command(name)
		if realPath, err := os.Readlink(cmd.Path); err == nil {
			if realPath != cmd.Path {
				cmd.Path = realPath
			}
		}
		cmd.Env = append(cmd.Env, os.Environ()...)
		cmd.Env = append(cmd.Env, envs...)
		bs, err := cmd.CombinedOutput()
		fmt.Println(string(bs), err)
	}
}

func getExceptFuncs() []exceptFunc {
	return []exceptFunc{
		{
			name:    EventCoreDump,
			raise:   getRegexecFunc(EventCoreDump, "GOTRACEBACK=crash"), // 必须有该环境变量才能产生core dump
			recover: func() {},
		},
		{
			name:    EventOOM,
			raise:   getRegexecFunc(EventOOM),
			recover: func() {},
		},
		{
			name:    EventDiskSpace,
			raise:   getRegexecFunc(EventDiskSpace, "TEST_PATH="+defaultExceptConfig.DiskSpacePath, "RAISE=1"),
			recover: getRegexecFunc(EventDiskSpace, "TEST_PATH="+defaultExceptConfig.DiskSpacePath),
		},
		{
			name:    EventDiskRO,
			raise:   getRegexecFunc(EventDiskRO, "TEST_PATH="+defaultExceptConfig.DiskROPath, "RAISE=1"),
			recover: getRegexecFunc(EventDiskRO, "TEST_PATH="+defaultExceptConfig.DiskROPath),
		},
		{
			name:    EventKeyword,
			raise:   getRegexecFunc(EventKeyword, "TEST_PATH="+defaultExceptConfig.KeywordPath, "RAISE=1"),
			recover: getRegexecFunc(EventKeyword, "TEST_PATH="+defaultExceptConfig.KeywordPath),
		},
	}
}

func produceException(f exceptFunc, interval time.Duration) {
	log.Println("produce exception", f.name, "every", interval)
	isRecover := false
	ticker := time.NewTicker(interval)
	for {
		select {
		case <-ticker.C:
			if isRecover {
				f.recover()
			} else {
				f.raise()
			}

			isRecover = !isRecover
		}
	}
}

func ProduceExceptions(interval time.Duration, single string, c *ExceptConfig) {
	defaultExceptConfig = c
	log.Printf("exception config: %+v\n", defaultExceptConfig)
	for _, f := range getExceptFuncs() {
		if single != "" && f.name != single {
			continue
		}
		go produceException(f, interval)
	}
}
