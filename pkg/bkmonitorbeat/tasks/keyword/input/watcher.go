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
	"io"
	"sync/atomic"
	"time"

	"github.com/nxadm/tail"
	"github.com/nxadm/tail/watch"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword/input/file"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword/module"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	watch.POLL_DURATION = 100 * time.Millisecond
}

type FileWatcher struct {
	FileTail *tail.Tail
	quit     chan bool
	File     *file.File
}

func NewFileWatcher(f *file.File) (fw *FileWatcher, err error) {
	var t *tail.Tail
	// 未活跃的文件无需启动监听
	if !f.IsInactivated {
		t, err = newTail(f)
		if err != nil {
			return nil, err
		}
	}

	return &FileWatcher{
		FileTail: t,
		quit:     make(chan bool),
		File:     f,
	}, nil
}

func newTail(f *file.File) (*tail.Tail, error) {
	filename := f.State.Source
	newTail, err := tail.TailFile(filename,
		tail.Config{
			Follow:    true,
			Poll:      true, // 这里使用poll来发现文件是否有变更，而不是inotify，一个考虑是部分系统没有inotify，再一个是inotify在目录移除后，不会有文件变更的信号，导致tail库不会触发读取文件
			ReOpen:    false,
			MustExist: true,
			// TODO log
			// Logger:    &helper.LogProxy{},
			Logger:   tail.DiscardingLogger,
			Location: &tail.SeekInfo{Offset: f.State.Offset, Whence: io.SeekStart},
		},
	)
	if err != nil {
		logger.Errorf("tail file %s err", filename)
		return nil, err
	}
	logger.Infof("start tail file %s %d", filename, f.State.Offset)

	return newTail, nil
}

func (fw *FileWatcher) Start() {
	filename := fw.File.State.Source
	defer func() {
		if fw.File.IsDeleted {
			// 文件被删除，需要保留WatchFiles中的内容
			logger.Infof("filename=>[%s] has been deleted, quit loopCollect", filename)
		} else if fw.File.IsInactivated {
			// 文件长时间未写入数据，需要保留WatchFiles中的内容
			// 释放Tail
			fw.FileTail = nil
			logger.Info("filename=>[%s] has no write for long time, quit loopCollect, stop tail", filename)
		} else {
			logger.Info("filename=>[%s] has no task, clear WatchFiles, quit loopCollect", filename)
		}
	}()

	logger.Infof("start collecting %s from %d", filename, fw.File.State.Offset)
	fw.loopCollect()
}

func (fw *FileWatcher) Restart() error {
	t, err := newTail(fw.File)
	if err != nil {
		logger.Errorf("Restart tail file=>(%s) err=>(%v)", fw.File.State.Source, err)
		return err
	}

	fw.FileTail = t
	go fw.Start()
	return nil
}

func (fw *FileWatcher) loopCollect() {
	defer func() {
		// kill tail task
		fw.FileTail.Kill(nil)

		// close file
		go func() {
			// 如果是文件长时间未写入数据退出的，外面会将指针置为nil，这里需要加一层判断
			if fw.FileTail == nil {
				return
			}

			err := fw.FileTail.Stop()
			if err != nil {
				logger.Errorf("tail stop error, filename=>%s, error=>%v", fw.FileTail.Filename, err)
			}
		}()

		// read line until empty
		for {
			select {
			case <-fw.FileTail.Lines:
				logger.Infof("%s read last content, maybe closed or moved", fw.FileTail.Filename)
				goto ForEnd
			}
		}
	ForEnd:

		fw.FileTail.Cleanup() // file delete
		logger.Info("stop collecting %s", fw.FileTail.Filename)
	}()

	maxInactiveCount := int64(fw.File.State.TTL / time.Minute)

	tt := time.NewTicker(1 * time.Minute)
	defer tt.Stop()
	for {
		select {
		case line := <-fw.FileTail.Lines:
			if line == nil {
				// tail.Lines is closed and empty.
				err := fw.FileTail.Err()
				if err != nil {
					logger.Errorf("[watcher]tail %s ended with error: %v", fw.FileTail.Filename, err)
					// time.Sleep(1 * time.Second)
					// continue
				}
				logger.Info("[watcher]%s closed or moved", fw.FileTail.Filename)
				return
			}
			logger.Infof("got line (%s)", line.Text)

			// deliver content
			e := &module.LogEvent{
				Text: line.Text,
				Data: fw,
				File: fw.File,
			}

			offset, err := fw.FileTail.Tell()
			if err != nil {
				logger.Errorf("[watcher]tail error, filename=>%s, error=>%v", fw.FileTail.Filename, err)
				continue
			}
			fw.File.State.Offset = offset

			var outputs []chan<- interface{}
			fw.File.Tasks.Range(func(k interface{}, v interface{}) bool {
				task := v.(*keyword.TaskConfig)
				select {
				case <-task.Ctx.Done():
					logger.Info("[watcher] task has done, delete it from input %v", fw.FileTail.Filename)
					fw.File.Tasks.Delete(task.TaskID)
				default:
					outputs = append(outputs, task.IPLinker)
				}

				return true
			})

			if len(outputs) == 0 {
				logger.Info("[watcher]file has not tasks, filename=>%v", fw.FileTail.Filename)
				return
			}

			// transfer new msg to next nodes
			for _, output := range outputs {
				output <- e
			}

			atomic.AddUint64(&CounterRead, 1)

			fw.File.State.Inactive = 0
		case <-tt.C:
			// 1. 文件长时间没有写入，则退出loop
			logger.Info("[watcher] loopCollect time tick tick tick %s", fw.FileTail.Filename)
			fw.File.State.Inactive++
			if fw.File.State.Inactive >= maxInactiveCount {
				// 每分钟检测文件是否还有新数据写入，如果超过一定时间没有更新，则释放文件
				fw.File.IsInactivated = true
				logger.Info("[watcher]%s without new line in %v, will close", fw.FileTail.Filename, fw.File.State.TTL)
				return
			}

			// 2. 如果文件被删除，则退出loop
			if fw.File.IsDeleted {
				logger.Info("[watcher]%s file has deleted, will close", fw.FileTail.Filename)
				return
			}

			// 3. 文件对应的任务列表，全被停止后。则退出loop
			var outputs []chan<- interface{}
			fw.File.Tasks.Range(func(k interface{}, v interface{}) bool {
				task := v.(*keyword.TaskConfig)
				select {
				case <-task.Ctx.Done():
					fw.File.Tasks.Delete(task.TaskID)
				default:
					outputs = append(outputs, task.IPLinker)
				}
				return true
			})
			if len(outputs) == 0 {
				logger.Info("[watcher]file has not tasks, filename=>%v", fw.FileTail.Filename)
				return
			}

		case <-fw.quit:
			return
		}
	}
}
