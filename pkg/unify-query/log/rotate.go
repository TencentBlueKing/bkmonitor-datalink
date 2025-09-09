// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package log

import (
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/pkg/errors"
)

// ReopenableWriteSyncer
type ReopenableWriteSyncer struct {
	filePath string       // file path
	cur      atomic.Value // *os.File
}

// NewReopenableWriteSyncer
func NewReopenableWriteSyncer(path string) (*ReopenableWriteSyncer, error) {
	var (
		file *os.File
		err  error
	)

	syncer := &ReopenableWriteSyncer{
		filePath: path,
		cur:      atomic.Value{},
	}

	// 增加判断，如果没有指定文件则走标准输出 os.stdout
	if path != "" {
		if file, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666); err != nil {
			fmt.Printf("failed to open file for->[%s]", err)
			return nil, errors.Wrapf(err, "open file failed")
		}
	} else {
		file = os.Stdout
	}

	syncer.cur.Store(file)
	return syncer, nil
}

// getFile
func (ws *ReopenableWriteSyncer) getFile() *os.File {
	return ws.cur.Load().(*os.File)
}

// Reload
func (ws *ReopenableWriteSyncer) Reload() error {
	// 先主动关闭当前文件，释放fd
	currentFile := ws.getFile()
	_ = currentFile.Close()

	// 重新打开新的文件
	f, err := os.OpenFile(ws.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		fmt.Printf("failed to open file->[%s] for->[%s]", ws.filePath, err)
		return err
	}
	ws.cur.Store(f)
	return nil
}

// sync调用时，需要明确的指定获取当前文件句柄同步
func (ws *ReopenableWriteSyncer) Sync() error {
	return ws.getFile().Sync()
}

// Write
func (ws *ReopenableWriteSyncer) Write(p []byte) (n int, err error) {
	return ws.getFile().Write(p)
}

// init
func init() {
	// 注册信号监听
	notify := make(chan os.Signal, 1)
	signal.Notify(notify, syscall.SIGHUP)

	go func() {
		for {
			// 如果有收到信号，则主动触发reload，重新打开文件
			<-notify
			if syncer != nil {
				if err := syncer.Reload(); err != nil {
					fmt.Printf("signal hup is receviced, but reopen file failed for->[%s]", err)
				}
			}
		}
	}()
}
