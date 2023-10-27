// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package notifier

import (
	"context"
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Notifier struct {
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
	period   time.Duration
	patterns []string
	mut      sync.Mutex
	digests  map[string]string
	ch       chan struct{}
}

func New(period time.Duration, patterns ...string) *Notifier {
	// 仅支持秒级
	if period.Seconds() < 1 {
		period = time.Minute
	}

	ctx, cancel := context.WithCancel(context.Background())
	notifier := &Notifier{
		ctx:      ctx,
		cancel:   cancel,
		period:   period,
		patterns: patterns,
		digests:  make(map[string]string),
		ch:       make(chan struct{}, 1),
	}

	go notifier.loopDetect()
	return notifier
}

func (n *Notifier) Close() {
	n.cancel()
	n.wg.Wait()
	close(n.ch)
}

func (n *Notifier) Ch() <-chan struct{} {
	return n.ch
}

func (n *Notifier) SetPattern(patterns ...string) {
	n.patterns = patterns
}

func (n *Notifier) loopDetect() {
	n.wg.Add(1)
	defer n.wg.Done()

	ticker := time.NewTicker(n.period)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			digests := make(map[string]string)
			for _, pattern := range n.patterns {
				n.detect(pattern, digests)
			}

			var changed bool
			for k, v := range digests {
				digest, ok := n.digests[k]
				if !ok || digest != v {
					changed = true
					break
				}
			}

			n.mut.Lock()
			n.digests = digests
			n.mut.Unlock()

			if changed {
				select {
				case n.ch <- struct{}{}:
				default:
				}
			}
		}
	}
}

func (n *Notifier) detect(pattern string, digests map[string]string) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		logger.Errorf("notifier: failed to fetch files, path=%s, err=%v", pattern, err)
		return
	}

	for _, match := range matches {
		b, err := os.ReadFile(match)
		if err != nil {
			logger.Errorf("notifier: failed to read file, path=%s, err=%v", match, err)
			continue
		}
		h := md5.New()
		h.Write(b)
		digests[fmt.Sprintf("%s/%s", pattern, match)] = fmt.Sprintf("%x", h.Sum(nil))
	}
}
