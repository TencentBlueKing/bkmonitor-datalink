// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/mattn/go-shellwords"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	// ErrScriptTimeout : the error indicate that script run timeout
	ErrScriptTimeout = fmt.Errorf("run script timeout")
)

// RunString Convert a shell command with a series of pipes into
// correspondingly piped list of *exec.Cmd
// If an arg has spaces, this will fail
func RunString(ctx context.Context, s string, userEnvs map[string]string) (string, error) {
	return runString(ctx, s, userEnvs, true)
}

// RunStringWithoutErr Convert a shell command with a series of pipes into
// correspondingly piped list of *exec.Cmd
// If an arg has spaces, this will fail
// not print error message
func RunStringWithoutErr(ctx context.Context, s string, userEnvs map[string]string) (string, error) {
	return runString(ctx, s, userEnvs, false)
}

func runString(ctx context.Context, s string, userEnvs map[string]string, withErr bool) (string, error) {
	startTime := time.Now().Unix()
	buf := bytes.NewBuffer([]byte{})
	sp, err := ParseCmdline2Cmds(s, userEnvs)
	if err != nil {
		return "", fmt.Errorf("parse command line %s failed:%s", s, err.Error())
	}
	for _, line := range sp {
		logger.Infof("get cmdline: %v", line)
		for _, item := range line {
			logger.Infof("item:%s", item)
		}
	}
	notify := make(chan error, 1)
	defer close(notify)

	cmds := make([]*exec.Cmd, len(sp))
	// create the commands
	for i, cs := range sp {
		cmd := cmdFromStrings(ctx, cs, userEnvs)
		cmds[i] = cmd
	}

	if withErr {
		cmds = AssemblePipes(cmds, nil, buf)
	} else {
		cmds = AssemblePipesWithoutErr(cmds, nil, buf)
	}

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		RunCmds(cmds, notify)
		wg.Done()
	}()

	select {
	case <-ctx.Done():
		for _, cmd := range cmds {
			if cmd != nil && cmd.Process != nil {
				logger.Infof("timeout to kill process pid %d, start at %d, cost %d",
					cmd.Process.Pid, startTime, time.Now().Unix()-startTime)
				if err := processGroupKill(cmd); err != nil {
					logger.Errorf("failed to kill process pid %d, because:%s, start at %d, cost %d",
						cmd.Process.Pid, err.Error(), startTime, time.Now().Unix()-startTime)
				} else {
					// wait for run command exit
					time.Sleep(2 * time.Second)
				}
			}
		}
		b := buf.Bytes()
		wg.Wait()
		return string(b), ErrScriptTimeout
	case err, ok := <-notify:
		if !ok || err != nil {
			return buf.String(), err
		}
		logger.Infof("script %v normal exist", sp)
		break
	}
	b := buf.Bytes()
	wg.Wait()
	return string(b), nil
}

func isSpace(r byte) bool {
	switch r {
	case ' ', '\t', '\r', '\n', '|':
		return true
	}
	return false
}

// GetEnver :
type GetEnver struct {
	userEnvs map[string]string
}

// GetEnv :
func (ge *GetEnver) GetEnv(key string) string {
	logger.Infof("to get environment %s, user env map %+v", key, ge.userEnvs)
	envValue, ok := ge.userEnvs[key]
	if ok {
		return envValue
	}
	return os.Getenv(key)
}

// ParseCmdline2Cmds parse command line to serial of sub command
func ParseCmdline2Cmds(cmdline string, userEnvs map[string]string) ([][]string, error) {
	cmds := make([][]string, 0)
	ger := &GetEnver{userEnvs: userEnvs}
	parser := shellwords.NewParser()
	parser.ParseEnv = true
	parser.Getenv = ger.GetEnv
	for {
		args, err := parser.Parse(cmdline)
		if err != nil {
			return cmds, err
		}
		cmds = append(cmds, args)
		if parser.Position < 0 {
			break
		}
		i := parser.Position
		for ; i < len(cmdline); i++ {
			if isSpace(cmdline[i]) {
				break
			}
		}
		cmdline = string([]rune(cmdline)[i+1:])
	}
	return cmds, nil
}

func cmdFromStrings(cmdCtx context.Context, cs []string, userEnvs map[string]string) *exec.Cmd {
	var cmd *exec.Cmd
	if len(cs) == 1 {
		cmd = exec.CommandContext(cmdCtx, cs[0])
	} else if len(cs) == 2 {
		cmd = exec.CommandContext(cmdCtx, cs[0], cs[1])
	} else {
		cmd = exec.CommandContext(cmdCtx, cs[0], cs[1:]...)
	}
	setProcessGroupID(cmd)
	// pass env to exec process
	cmd.Env = os.Environ()
	for k, v := range userEnvs {
		envItem := k + "=" + v
		cmd.Env = append(cmd.Env, envItem)
	}
	return cmd
}

// AssemblePipes Pipe stdout of each command into stdin of next
func AssemblePipes(cmds []*exec.Cmd, stdin io.Reader, stdout io.Writer) []*exec.Cmd {
	return assemblePipes(cmds, stdin, stdout, true)
}

// AssemblePipesWithoutErr Pipe stdout of each command into stdin of next and not use stderr
func AssemblePipesWithoutErr(cmds []*exec.Cmd, stdin io.Reader, stdout io.Writer) []*exec.Cmd {
	return assemblePipes(cmds, stdin, stdout, false)
}

func assemblePipes(cmds []*exec.Cmd, stdin io.Reader, stdout io.Writer, withErr bool) []*exec.Cmd {
	cmds[0].Stdin = stdin
	if withErr {
		cmds[0].Stderr = stdout
	}

	// assemble pipes
	for i, c := range cmds {
		if i < len(cmds)-1 {
			cmds[i+1].Stdin, _ = c.StdoutPipe()
			if withErr {
				cmds[i+1].Stderr = stdout
			}
		} else {
			c.Stdout = stdout
			if withErr {
				c.Stderr = stdout
			}

		}
	}
	return cmds
}

// RunCmds Run series of piped commands
func RunCmds(cmds []*exec.Cmd, notify chan error) {
	var e error
	defer func() {
		notify <- e
	}()
	startTime := time.Now().Unix()
	// start processes in descending order
	for i := len(cmds) - 1; i > 0; i-- {
		if err := cmds[i].Start(); err != nil {
			e = err
			return
		}
	}
	// run the first process
	if err := cmds[0].Run(); err != nil {
		e = err
		return
	}
	// wait on processes in ascending order
	for i := 1; i < len(cmds); i++ {
		if err := cmds[i].Wait(); err != nil {
			e = err
			return
		}
	}
	logger.Infof("after command wait, start at %d, cost time %d", startTime, time.Now().Unix()-startTime)
}
