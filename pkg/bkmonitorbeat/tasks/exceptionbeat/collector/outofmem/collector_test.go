// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package outofmem

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
)

func TestOutOfMemCollector_WatchOOMEvents(t *testing.T) {
	type fields struct {
		dataid    int
		state     int
		oomch     chan *OOMInfo
		oomctx    context.Context
		ctxCancel context.CancelFunc
		startup   int64
		reportGap time.Duration
	}
	type args struct {
		ctx context.Context
		e   chan<- define.Event
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			"test total",
			fields{
				startup:   time.Now().Unix(),
				reportGap: 10 * time.Millisecond,
			},
			args{
				e: make(chan define.Event),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.fields.oomch = make(chan *OOMInfo, 100)
			tt.fields.oomctx, tt.fields.ctxCancel = context.WithCancel(context.Background())
			c := &OutOfMemCollector{
				dataid:    tt.fields.dataid,
				state:     tt.fields.state,
				oomch:     tt.fields.oomch,
				oomctx:    tt.fields.oomctx,
				ctxCancel: tt.fields.ctxCancel,
				startup:   tt.fields.startup,
				reportGap: tt.fields.reportGap,
			}
			eventChan := make(chan define.Event)
			// 上报间隔1/10作为一个事件周期
			timeElem := tt.fields.reportGap / 10
			// 50事件周期后退出
			ctx, _ := context.WithTimeout(context.Background(), 50*timeElem)
			tt.args.e = eventChan
			tt.args.ctx = ctx
			// 30事件周期后停止发送
			sendTimeout := 30 * timeElem
			time.AfterFunc(sendTimeout, tt.fields.ctxCancel)
			go c.WatchOOMEvents(tt.args.ctx, tt.args.e)
			var count uint64 = 0
			go func() {
				sendTicker := time.NewTicker(timeElem)
			LoopSend:
				for {
					select {
					case <-sendTicker.C:
						e := &OOMInfo{
							OomInstance: &OomInstance{
								TimeOfDeath: time.Now(),
								ProcessName: "testProc",
							},
						}
						tt.fields.oomch <- e
						count++
					case <-tt.fields.oomctx.Done():
						break LoopSend
					}
				}
			}()
			i := 0
			var sum uint64 = 0
		LoopGet:
			for {
				select {
				case v := <-eventChan:
					value, _ := v.AsMapStr().GetValue("value")
					if events, ok := value.([]beat.MapStr); ok {
						if len(events) != 1 {
							t.Fail()
						}
						extra, _ := events[0].GetValue("extra")
						total, _ := extra.(beat.MapStr).GetValue("total")
						if totalValue, ok := total.(uint64); ok {
							sum += totalValue
						} else {
							t.Errorf("invalid total value:  %+v", total)
						}
					} else {
						t.Errorf("invalid event value:  %+v", value)
					}
					i++
				case <-ctx.Done():
					break LoopGet
				}
			}
			if sum != count {
				t.Errorf("sum: want %v got %v", count, sum)
			}
			if i != 4 && i != 3 {
				t.Errorf("send times: want 3 or 4 got %v", i)
			}
		})
	}
}
