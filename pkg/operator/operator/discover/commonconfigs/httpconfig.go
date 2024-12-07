// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package commonconfigs

import (
	"github.com/elastic/beats/libbeat/common/transport/tlscommon"
	"github.com/prometheus/common/config"
)

func WrapHttpAccessBasicAuth(c config.HTTPClientConfig) func() (string, string, error) {
	return func() (string, string, error) {
		auth := c.BasicAuth
		if auth != nil {
			return auth.Username, string(auth.Password), nil
		}
		return "", "", nil
	}
}

func WrapHttpAccessBearerToken(c config.HTTPClientConfig) func() (string, error) {
	return func() (string, error) {
		auth := c.Authorization
		if auth == nil || auth.Type != "Bearer" {
			return "", nil
		}
		return string(auth.Credentials), nil
	}
}

func WrapHttpAccessTLSConfig(c config.HTTPClientConfig) func() (*tlscommon.Config, error) {
	return func() (*tlscommon.Config, error) {
		cfg := c.TLSConfig
		if len(cfg.CAFile) == 0 && len(cfg.KeyFile) == 0 && len(cfg.CertFile) == 0 {
			return nil, nil
		}

		tlsConfig := &tlscommon.Config{
			CAs: []string{cfg.CAFile},
		}

		tlsConfig.Certificate.Certificate = cfg.CertFile
		tlsConfig.Certificate.Key = cfg.KeyFile
		return tlsConfig, nil
	}
}
