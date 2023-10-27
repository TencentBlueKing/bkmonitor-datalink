// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
)

// NewStaticScheduler :
func NewStaticScheduler(ctx context.Context, name string) (*Scheduler, error) {
	return NewScheduler(ctx, name, func(ctx context.Context) <-chan *define.WatchEvent {
		var task struct {
			Tasks []*config.PipelineConfig `mapstructure:"tasks" json:"tasks"`
		}
		conf := config.FromContext(ctx)
		ch := make(chan *define.WatchEvent)
		go func() {
			if !conf.IsSet(define.ConfPipeline) {
				logging.Infof("no pipelines found in configuration")
				return
			}
			logging.PanicIf(conf.UnmarshalKey(define.ConfPipeline, &task))

			pipelines := task.Tasks
			logging.Infof("read %d pipelines from config", len(pipelines))

		loop:
			for _, pipe := range pipelines {
				ev := &define.WatchEvent{
					Data: pipe,
					Type: define.WatchEventAdded,
				}
				select {
				case ch <- ev:
					logging.Infof("pipeline %d sent", pipe.DataID)
					continue
				case <-ctx.Done():
					break loop
				}
			}
		}()
		return ch
	})
}

func init() {
	define.RegisterScheduler("static", func(ctx context.Context, name string) (scheduler define.Scheduler, e error) {
		return NewStaticScheduler(ctx, name)
	})
}
