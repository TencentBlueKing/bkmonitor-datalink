// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errno"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// 服务侧初始化consul实例使用
type Service struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
}

// Type
func (s *Service) Type() string {
	return "consul"
}

// Start
func (s *Service) Start(ctx context.Context) {
	s.Reload(ctx)
}

// Reload
func (s *Service) Reload(ctx context.Context) {
	// 关闭上一次的consul instance
	if s.cancelFunc != nil {
		s.cancelFunc()
	}

	log.Debugf(context.TODO(), "waiting for consul service close")
	// 等待上一个注册彻底关闭
	s.Wait()

	// 更新上下文控制方法
	s.ctx, s.cancelFunc = context.WithCancel(ctx)
	log.Debugf(context.TODO(), "consul service context update success.")

	err := consul.SetInstance(
		s.ctx, KVBasePath, ServiceName, Address, []string{"unify-query"},
		HTTPAddress, Port, TTL, CaFilePath, KeyFilePath, CertFilePath,
	)
	if err != nil {
		codedErr := errno.ErrStorageConnFailed().
			WithComponent("Consul服务").
			WithOperation("初始化Consul实例").
			WithContext("service_name", ServiceName).
			WithContext("address", Address).
			WithContext("http_address", HTTPAddress).
			WithContext("error", err.Error()).
			WithSolution("检查Consul服务器连接配置")
		log.ErrorWithCodef(context.TODO(), codedErr)
		return
	}
	err = consul.LoopAwakeService()
	if err != nil {
		codedErr := errno.ErrBusinessLogicError().
			WithComponent("Consul服务").
			WithOperation("启动服务保活循环").
			WithContext("service_name", ServiceName).
			WithContext("error", err.Error()).
			WithSolution("检查Consul服务注册和保活机制")
		log.ErrorWithCodef(context.TODO(), codedErr)
		return
	}

	codedInfo := errno.ErrInfoServiceStart().
		WithComponent("Consul").
		WithOperation("服务启动").
		WithContext("状态", "成功")
	log.InfoWithCodef(context.TODO(), codedInfo)
}

// Wait
func (s *Service) Wait() {
	consul.Wait()
}

// Close
func (s *Service) Close() {
	s.cancelFunc()
	log.Infof(context.TODO(), "consul service context cancel func called.")
}
