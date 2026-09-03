// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafkaclient

import (
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

// ClientOptions 构造 consumer 与 producer 共用的 broker、client ID、TLS 和 SASL 选项。
func ClientOptions(brokers []string, clientID string, security SecurityConfig) ([]kgo.Opt, error) {
	normalizedBrokers, err := NormalizeBrokers(brokers)
	if err != nil {
		return nil, err
	}
	if err := ValidateClientID(clientID); err != nil {
		return nil, err
	}
	security = security.WithDefaults()
	tlsConfig, err := security.BuildTLSConfig()
	if err != nil {
		return nil, err
	}

	options := []kgo.Opt{kgo.SeedBrokers(normalizedBrokers...)}
	if clientID != "" {
		options = append(options, kgo.ClientID(clientID))
	}
	if tlsConfig != nil {
		options = append(options, kgo.DialTLSConfig(tlsConfig))
	}
	if security.SASL != nil {
		auth := security.SASL
		switch auth.Mechanism {
		case SASLMechanismPlain:
			options = append(options, kgo.SASL(plain.Auth{User: auth.Username, Pass: auth.Password}.AsMechanism()))
		case SASLMechanismSCRAMSHA256:
			options = append(options, kgo.SASL(scram.Auth{User: auth.Username, Pass: auth.Password}.AsSha256Mechanism()))
		case SASLMechanismSCRAMSHA512:
			options = append(options, kgo.SASL(scram.Auth{User: auth.Username, Pass: auth.Password}.AsSha512Mechanism()))
		default:
			return nil, fmt.Errorf("unsupported SASL mechanism after validation: %q", auth.Mechanism)
		}
	}
	return options, nil
}
