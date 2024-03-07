// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package input

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"testing"
	"time"

	"github.com/elastic/beats/libbeat/common/atomic"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword/module"
)

const tempDirPattern = "test-keyword-*"

type mockService struct {
	ctx      context.Context
	msgCount atomic.Int
	logPath  string
	cancel   func()
}

func init() {
	ScanTickerDuration = time.Second
}

func waitScan() {
	time.Sleep(ScanTickerDuration + time.Second)
}

func waitCollect() {
	time.Sleep(time.Second)
}

func newMockService() *mockService {
	logPath, err := os.MkdirTemp("", tempDirPattern)
	check(err)
	ctx, cancel := context.WithCancel(context.Background())
	return &mockService{
		ctx:     ctx,
		logPath: logPath,
		cancel:  cancel,
	}
}

func (s *mockService) start() {
	var err error
	next := make(chan interface{})

	go func() {
		for {
			select {
			case msg := <-next:
				s.msgCount.Inc()
				fmt.Println("file:", msg.(*module.LogEvent).Data.(*FileWatcher).FileTail.Filename)
				fmt.Println("get msg:", msg.(*module.LogEvent).Text, "current count:", s.msgCount.Load())
			}
		}
	}()

	taskCtx := context.WithValue(s.ctx, "taskID", "IamTaskId123")
	cfg := map[string]*keyword.TaskConfig{
		"IamTaskId123": {
			Input: keyword.InputConfig{
				Paths:         []string{path.Join(s.logPath, "*.log*")},
				ScanFrequency: 1 * time.Millisecond,
				CloseInactive: 1 * time.Hour,
			},
			IPLinker:  next,
			RawText:   configs.KeywordTaskConfig{RetainFileBytes: configs.DefaultRetainFileBytes},
			Ctx:       taskCtx,
			CtxCancel: s.cancel,
		},
	}

	input, err := New(s.ctx, cfg, nil)
	if err != nil {
		panic(err)
	}
	input.AddOutput(next)
	err = input.Start()
	check(err)
}

func (s *mockService) close() {
	err := os.RemoveAll(s.logPath)
	check(err)
	s.cancel()
}

func appendFileln(filename string, content string) {
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
	check(err)
	defer func() {
		err = f.Close()
		check(err)
	}()

	if len(content) > 0 {
		_, err = f.WriteString(content + "\n")
	}
	check(err)
}

func newEmptyFile(filename string) {
	f, err := os.Create(filename)
	check(err)
	defer func() {
		err = f.Close()
		check(err)
	}()
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func copyfile(src, dst string) {
	srcFile, err := os.Open(src)
	check(err)
	defer func() { _ = srcFile.Close() }()

	destFile, err := os.Create(dst) // creates if file doesn't exist
	check(err)
	defer func() { _ = destFile.Close() }()

	_, err = io.Copy(destFile, srcFile) // check first var for number of bytes copied
	check(err)

	err = destFile.Sync()
	check(err)
}

func checkRes(s *mockService, expect int, t *testing.T) {
	waitCollect()
	actual := s.msgCount.Load()
	if actual != expect {
		t.Error(fmt.Sprintf("collect %d, want %d", actual, expect))
	}
}

func Test_Truncate(t *testing.T) {
	t.Parallel()
	s := newMockService()
	filename := path.Join(s.logPath, "truncate.log")
	newEmptyFile(filename)
	s.start()
	defer s.close()

	appendFileln(filename, "test truncate1")
	waitCollect()

	f, err := os.OpenFile(filename, os.O_WRONLY, 0o644)
	check(err)
	err = f.Truncate(0)

	// TODO github.com/hpcloud/tail has bug, write after truncated too fast will lost logs
	// https://github.com/hpcloud/tail/issues/145
	waitScan()

	check(err)
	appendFileln(filename, "test truncate2")
	appendFileln(filename, "test truncate3")

	checkRes(s, 3, t)
}

func Test_NewFile(t *testing.T) {
	t.Parallel()
	s := newMockService()
	file := path.Join(s.logPath, "1.log")
	newEmptyFile(file)
	s.start()
	defer s.close()
	appendFileln(file, "test new")
	checkRes(s, 1, t)
}

func Test_CP(t *testing.T) {
	t.Parallel()
	s := newMockService()
	// cp 1.log 2.log
	file1 := path.Join(s.logPath, "1.log")
	file2 := path.Join(s.logPath, "2.log")
	newEmptyFile(file1)
	s.start()
	defer s.close()
	appendFileln(file1, "test cp")

	copyfile(
		file1,
		file2,
	)
	waitScan()

	checkRes(s, 2, t)
}

func Test_RM(t *testing.T) {
	t.Parallel()
	s := newMockService()
	file := path.Join(s.logPath, "rm.log")
	newEmptyFile(file)
	s.start()
	defer s.close()
	appendFileln(file, "test rm 1")
	waitCollect()
	err := os.Remove(file)
	check(err)
	appendFileln(file, "test rm 2")
	waitScan()

	checkRes(s, 2, t)
}

func Test_MV(t *testing.T) {
	t.Parallel()
	s := newMockService()
	file := path.Join(s.logPath, "mv.log")
	newfile := path.Join(s.logPath, "mv.log.1")

	newEmptyFile(file)
	s.start()
	defer s.close()

	appendFileln(file, "test mv 1")
	waitCollect()
	err := os.Rename(file, newfile)
	check(err)
	appendFileln(file, "test mv 2")
	appendFileln(newfile, "test mv 3")
	waitScan()
	checkRes(s, 3, t)
}

func Test_Append(t *testing.T) {
	t.Parallel()
	s := newMockService()

	filename := path.Join(s.logPath, "append.log")

	newEmptyFile(filename)
	s.start()
	defer s.close()
	appendFileln(filename, "test append1")
	appendFileln(filename, "test append2")
	checkRes(s, 2, t)
}
