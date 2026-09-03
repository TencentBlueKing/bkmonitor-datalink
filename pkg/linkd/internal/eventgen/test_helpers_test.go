// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package eventgen

import (
	"io"
	"log/slog"

	linkdconfig "linkd/internal/config"
	"linkd/internal/kafkaclient"
)

func testSource() linkdconfig.EventSource {
	return linkdconfig.EventSource{
		EventSourceID:    "eventgen-source",
		Enabled:          true,
		Cleaner:          linkdconfig.CleanerConfig{Type: linkdconfig.CleanerTypeStandard},
		FingerprintMode:  linkdconfig.FingerprintModeField,
		FingerprintField: "source_alert_id",
		Storage: linkdconfig.EventSourceStorageConfig{
			Type: linkdconfig.StorageTypeKafka,
			Kafka: linkdconfig.KafkaStorageConfig{
				Brokers: []string{"127.0.0.1:9092"}, Topic: "eventgen-events", ConsumerGroup: "eventgen-cleaner",
				Security: kafkaclient.SecurityConfig{Protocol: kafkaclient.SecurityProtocolPlaintext},
			},
		},
	}.WithDefaults()
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
