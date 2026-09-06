// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

import (
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// Gluttonous :
type Gluttonous struct{}

func (g *Gluttonous) Poll() time.Duration { return 0 }

func (g *Gluttonous) SetPoll(t time.Duration) {}

func (g *Gluttonous) SetETLRecordFields(f *define.ETLRecordFields) {}

// Process : process data
func (g *Gluttonous) Process(p define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
}

// Push : process data
func (g *Gluttonous) Push(d define.Payload, killChan chan<- error) {
}

// String : return Backend name
func (g *Gluttonous) String() string {
	return "gluttonous"
}

// Commit : commit check point
func (g *Gluttonous) Commit() error {
	return nil
}

// Reset : reset Backend
func (g *Gluttonous) Reset() error {
	return nil
}

func (g *Gluttonous) SetIndex(i int) {}

func (g *Gluttonous) Index() int { return 0 }

// Finish : process finished
func (g *Gluttonous) Finish(outputChan chan<- define.Payload, killChan chan<- error) {
}

// Close : close Backend
func (g *Gluttonous) Close() error {
	return nil
}

// NewGluttonous :
func NewGluttonous() *Gluttonous {
	return &Gluttonous{}
}

// NewGluttonousNode :
func NewGluttonousNode(ctx context.Context) Node {
	ctx, cancel := context.WithCancel(ctx)
	return NewBackendNode(ctx, cancel, NewGluttonous())
}

func init() {
	define.RegisterDataProcessor("gluttonous-processor", func(ctx context.Context, name string) (define.DataProcessor, error) {
		return NewGluttonous(), nil
	})
	define.RegisterBackend("gluttonous", func(ctx context.Context, name string) (define.Backend, error) {
		return NewGluttonous(), nil
	})
}
