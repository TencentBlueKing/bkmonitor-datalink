// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package filewatcher

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var defaultWatcher = New()

func Stop() {
	defaultWatcher.Stop()
}

func AddPath(path string) (<-chan struct{}, error) {
	return defaultWatcher.AddPath(path)
}

func RemovePath(path string) error {
	return defaultWatcher.RemovePath(path)
}

const defaultPeriod = 5 * time.Second

// Watcher 文件变化监视器
type Watcher struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mut           sync.Mutex
	watcherCancel map[string]context.CancelFunc
}

func New() *Watcher {
	ctx, cancel := context.WithCancel(context.Background())
	return &Watcher{
		ctx:           ctx,
		cancel:        cancel,
		watcherCancel: make(map[string]context.CancelFunc),
	}
}

func (w *Watcher) Stop() {
	w.mut.Lock()
	defer w.mut.Unlock()

	for file, cancel := range w.watcherCancel {
		cancel()
		delete(w.watcherCancel, file)
	}
	w.cancel()
	w.wg.Wait()
}

func (w *Watcher) AddPath(file string) (<-chan struct{}, error) {
	// 先检查一下文件能不能读
	if _, err := os.Stat(file); err != nil {
		return nil, err
	}

	w.mut.Lock()
	defer w.mut.Unlock()

	ch := make(chan struct{}, 1)
	subCtx, cancel := context.WithCancel(w.ctx)
	w.startWatch(subCtx, file, ch)
	w.watcherCancel[file] = cancel

	logger.Infof("watcher add new path %s", file)
	return ch, nil
}

func (w *Watcher) RemovePath(file string) error {
	w.mut.Lock()
	defer w.mut.Unlock()

	if cancel, ok := w.watcherCancel[file]; !ok {
		return fmt.Errorf("watcher file %s not watching", file)
	} else {
		cancel()
		delete(w.watcherCancel, file)
	}
	return nil
}

func (w *Watcher) getModTime(file string) (time.Time, error) {
	fileInfo, err := os.Stat(file)
	if err != nil {
		return time.Now(), err
	}
	return fileInfo.ModTime(), nil
}

func (w *Watcher) startWatch(ctx context.Context, file string, ch chan<- struct{}) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()

		ticker := time.NewTicker(defaultPeriod)
		defer ticker.Stop()

		var updatedAt time.Time
		for {
			select {
			case <-ctx.Done():
				logger.Infof("watcher task %s stopped", file)
				return

			case <-ticker.C:
				modTime, err := w.getModTime(file)
				if err != nil {
					logger.Errorf("watcher get mod time failed, file=%s, err: %s", file, err)
					continue
				}

				if modTime != updatedAt {
					logger.Infof("watcher file %s changed, publish signal", file)
					ch <- struct{}{}
					updatedAt = modTime
				}
			}
		}
	}()
}
