// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package utils_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
)

// NOTE: these tests don't actually test anything
// they require manual verification ...
// TODO something about this!

func TestAssemblePipes(t *testing.T) {
	cmd1 := exec.Command("ps", "aux")
	cmd2 := exec.Command("grep", "usr")
	cmd3 := exec.Command("awk", "{print $2}")
	buf := bytes.NewBuffer([]byte{})
	cmds := []*exec.Cmd{cmd1, cmd2, cmd3}
	utils.AssemblePipes(cmds, os.Stdin, os.Stdout)
	notify := make(chan error, 1)
	defer close(notify)
	utils.RunCmds(cmds, notify)
	err := <-notify
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(string(buf.Bytes()))
}

func TestString(t *testing.T) {
	cmdCtx, cmdCancel := context.WithTimeout(context.Background(), 3*time.Second)
	// releases resources if execCmd completes before timeout elapses
	defer cmdCancel()
	s, err := utils.RunString(cmdCtx, "ps aux | grep usr", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(s)
}
