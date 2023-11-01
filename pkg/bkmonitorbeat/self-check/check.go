// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package self_check

import (
	"flag"
	"fmt"

	"github.com/fatih/color"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/self-check/config"
)

const (
	EmptyTestType = ""
	FullyTestType = "FullyTest" // 全量检测
)

type CheckConf struct {
	ConfigPath string // 配置文件地址
	CheckType  string // 检测类型
}

// TestFunc 自定义类型用于描述测试功能
type TestFunc = func()

var (
	CheckType  = "check.type"
	ConfigPath = "check.config.path"
	configArgs = CheckConf{}
	testMap    = map[string]TestFunc{}
)

// RegisterTestMap 注册功能测试函数
func RegisterTestMap(name string, f TestFunc) {
	testMap[name] = f
}

// GetAllTestFunc 获取所有的测试函数，预留功能输出所有的检测函数
func GetAllTestFunc() []string {
	allFunc := make([]string, len(testMap))
	for k, _ := range testMap {
		allFunc = append(allFunc, k)
	}
	return allFunc
}

// selectTestMap 返回测试功能函数
func selectTestMap(name string) (TestFunc, error) {
	if name == "" {
		return nil, errors.New("test func not found, name is empty\n")
	}
	f, ok := testMap[name]
	if !ok {
		return nil, errors.New("test func not found.\n")
	}
	return f, nil
}

// fullyTest 全量检测
func fullyTest() {
	for k, _ := range testMap {
		componentTest(k)
	}
}

// componentTest 补分功能检测
func componentTest(name string) {
	color.Yellow("start to check component: %s\n\n", name)
	f, err := selectTestMap(name)
	if err != nil {
		color.Red(err.Error())
	}
	// 开始执行组件功能测试
	f()
	color.Yellow("\ncomponent: %s test finished!\n", name)
}

func DoSelfCheck() {
	config.ParseConfiguration()
	switch configArgs.CheckType {
	case EmptyTestType:
		fmt.Println(`please input test type like 'FullyTest or QuickTest' or component name like 'basereport、ping...'`)
	case FullyTestType:
		fullyTest()
	default:
		componentTest(configArgs.CheckType)
	}
}

func init() {
	flag.StringVar(&configArgs.CheckType, CheckType, "QuickTest", "check.type: FullyTest、QuickTest. (you can also input component name like 'basereport' 'http' 'ping' etc. )")
	flag.StringVar(&configArgs.ConfigPath, ConfigPath, "../etc/bkmonitorbeat.conf", "check.config.path: Location of configuration file（Path relative to the current executable file）")
}
