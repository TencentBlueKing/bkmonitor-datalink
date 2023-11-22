// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package exec

import (
	"log"
)

const (
	execScript = "script"
)

var AllExecNames = []string{
	execScript,
}

type TestExecConfig struct {
	PromPort int
}

func getExecFuncs(c *TestExecConfig) map[string]func() {
	return map[string]func(){
		execScript: func() {
			err := runScript(c.PromPort)
			if err != nil {
				log.Fatalln(err)
			}
		},
	}
}

func RunExec(name string, c *TestExecConfig) {
	if f, ok := getExecFuncs(c)[name]; ok {
		f()
	}
}
