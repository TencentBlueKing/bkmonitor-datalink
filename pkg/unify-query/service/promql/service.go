// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql

import (
	"context"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
)

var EnginePool *sync.Pool

// Service 服务侧初始化consul实例使用
type Service struct {
}

// Type
func (s *Service) Type() string {
	return "promql"
}

// Start
func (s *Service) Start(_ context.Context) {
	params := &promql.Params{
		Timeout:              Timeout,
		LookbackDelta:        LookbackDelta,
		MaxSamples:           MaxSamples,
		EnableNegativeOffset: EnableNegativeOffset,
		EnableAtModifier:     EnableAtModifier,
	}
	promql.NewEngine(params)

	EnginePool = &sync.Pool{
		New: func() interface{} {
			// 在这里创建并返回一个新的对象
			return promql.NewCalEngine(params)
		},
	}

	for i := 0; i < MaxEngineNum-1; i++ {
		go func() {
			promqlEngine := promql.NewCalEngine(params)
			EnginePool.Put(promqlEngine)
		}()
	}
	promql.SetDefaultStep(DefaultStep)
}

// Reload
func (s *Service) Reload(ctx context.Context) {
	s.Close()
	s.Wait()
	s.Start(ctx)
}

// Wait
func (s *Service) Wait() {
}

// Close
func (s *Service) Close() {
	log.Infof(context.TODO(), "promql service context canceled")
}
