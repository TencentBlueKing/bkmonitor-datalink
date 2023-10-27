// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package reloader

// use named pipe for ipc

import (
	"bufio"
	"fmt"
	"time"

	"github.com/elastic/beats/libbeat/cfgfile"
	"github.com/elastic/beats/libbeat/logp"
	"github.com/natefinch/npipe"
)

const (
	namedPipe  = "_win_ipc_pipe"
	sigReload  = "bkreload"
	sigReload2 = "bkreload2"
)

// Run runs the reloader
func (rl *Reloader) Run(path string) error {
	logp.Info("Config reloader started")
	// listen
	// ln, err := npipe.Listen(`\\.\pipe\` + rl.name + namedPipe)
	ln, err := npipe.Listen(`\\.\pipe\` + rl.name + namedPipe + path)
	if err != nil {
		return err
	}

	go rl.signalHandler(ln)
	return nil
}

// Stop stops the reloader and waits for all modules to properly stop
func (rl *Reloader) Stop() {
	close(rl.done)
}

func (rl *Reloader) signalHandler(ln *npipe.PipeListener) {
	for {
		conn, err := ln.Accept()
		if err == npipe.ErrClosed {
			logp.Info("config reloader stopped")
			return
		}
		if err != nil {
			// handle error
			logp.Err("Error accepting connection: %v", err)
			continue
		}

		// handle connection like any other net.Conn
		r := bufio.NewReader(conn)
		msg, err := r.ReadString('\n')
		if err != nil {
			logp.Err("Error reading from server connection: %v", err)
			continue
		}
		if msg != rl.fd.(string)+"\n" {
			logp.Err("Read incorrect message. Expected '%s', got '%s'", rl.fd.(string), msg)
			continue
		}
		logp.Info("reloader recv msg=%s", msg)

		// close client
		if err := conn.Close(); err != nil {
			logp.Err("Error closing server side of connection: %v", err)
			continue
		}

		logp.Info("Reloading %s config", rl.name)

		// get new config
		c, err := cfgfile.Load("", nil)
		if err != nil {
			logp.Err("Error loading config: %s", err)
			continue
		}
		c, err = c.Child(rl.name, -1)
		if err != nil {
			logp.Err("Error loading config: %s", err)
			continue
		}
		logp.Info("reloader get config:%+v", c)

		rl.handler.Reload(c)
	}
}

// ReloadEvent send reload event
func ReloadEvent(name, path string) error {
	fmt.Print("sending reload msg...")

	// Caution: this is not normall path
	// conn, err := npipe.Dial(`\\.\pipe\` + name + namedPipe)
	conn, err := npipe.DialTimeout(`\\.\pipe\`+name+namedPipe+path, time.Nanosecond*1000000)
	if err != nil {
		fmt.Println("Fail")
		return err
	}
	defer conn.Close()

	// send msg
	if _, err := fmt.Fprintln(conn, sigReload); err != nil {
		fmt.Println("Fail")
		return err
	}
	fmt.Println("Done")
	return nil
}
