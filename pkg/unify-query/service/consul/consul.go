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
	"errors"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul/base"
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
	s.Close()

	// 更新上下文控制方法
	s.ctx, s.cancelFunc = context.WithCancel(ctx)
	log.Debugf(context.TODO(), "consul service context update success.")

	var auth *base.AuthConfig
	if viper.GetBool("kms.enabled") {
		auth = &base.AuthConfig{
			Token:    viper.GetString("consul.token"),
			Username: viper.GetString("consul.username"), Password: viper.GetString("consul.password"),
			KeyPEM: []byte(viper.GetString("consul.tls.key_pem")), CertPEM: []byte(viper.GetString("consul.tls.cert_pem")),
		}
	}
	err := consul.SetInstanceWithAuth(
		s.ctx, KVBasePath, Address, CaFilePath, KeyFilePath, CertFilePath, auth,
	)
	var addressErr *consul.AddressError
	if errors.As(err, &addressErr) && addressErr.Reason == consul.ReasonAddressNotConfigured {
		log.Infof(context.TODO(), "consul address is not configured, consul client is not created")
		return
	}
	if err != nil {
		// 地址缺 host 等配置错误只在加载时报一次，之后的调用直接返回同一原因，不连接任何地址。
		log.Errorf(context.TODO(), "consul service init failed for->[%s]", err)
		return
	}

	log.Warnf(context.TODO(), "consul KV client initialized.")
}

// Wait satisfies the service lifecycle; this client has no background registration.
func (s *Service) Wait() {}

// Close
func (s *Service) Close() {
	if s.cancelFunc != nil {
		s.cancelFunc()
	}
	consul.Close()
	log.Infof(context.TODO(), "consul service context cancel func called.")
}
