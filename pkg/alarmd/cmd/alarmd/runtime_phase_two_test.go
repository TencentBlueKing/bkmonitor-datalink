// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestPhaseTwoApplicationHealthUsesWorkerReadinessWithoutKafkaInputState(t *testing.T) {
	health := newPhaseTwoApplicationHealth()
	health.Update(phaseTwoReadiness{
		State: observability.HealthReady, SnapshotReady: true, AssignmentReady: true,
		RuntimeStateReady: true, OutputSinkReady: true,
	})

	snapshot := health.HealthSnapshot()
	if !snapshot.PhaseTwo || !snapshot.Ready {
		t.Fatalf("phase-two health = %+v, want ready phase-two worker", snapshot)
	}
	if snapshot.AssignedClaims != 0 || snapshot.ConsumerLagKnown || snapshot.ConsumerLagRecords != 0 {
		t.Fatalf("phase-two health leaked Kafka input claim/lag state: %+v", snapshot)
	}
}

func TestPhaseTwoApplicationHealthRequiresSnapshotAndWorkerPrerequisites(t *testing.T) {
	health := newPhaseTwoApplicationHealth()
	health.Update(phaseTwoReadiness{
		State: observability.HealthReady, SnapshotReady: true, AssignmentReady: true,
		RuntimeStateReady: true,
	})

	snapshot := health.HealthSnapshot()
	if snapshot.Ready || snapshot.State != observability.HealthNotReady {
		t.Fatalf("phase-two health = %+v, want not ready without output sink", snapshot)
	}
}

func TestNewPhaseTwoApplicationAcceptsOnlyGoAccess(t *testing.T) {
	goAccess := validGoAccessRuntimeConfig()
	application, err := newPhaseTwoApplication(goAccess)
	if err != nil {
		t.Fatalf("newPhaseTwoApplication() error = %v", err)
	}
	if snapshot := application.HealthSnapshot(); !snapshot.PhaseTwo || snapshot.State != observability.HealthStarting {
		t.Fatalf("new phase-two application health = %+v", snapshot)
	}

	compatibility := goAccess
	compatibility.Input = config.PhaseTwoInputConfig{
		Mode: config.InputModePhaseOneKafkaCompatibility,
		PhaseOneKafka: &config.PhaseOneKafkaCompatibilityConfig{
			InputTopic: "alarmd-input", ConsumerGroup: "alarmd", InitialOffset: "oldest", StatePrefix: "alarmd",
		},
	}
	if _, err := newPhaseTwoApplication(compatibility); err == nil {
		t.Fatal("newPhaseTwoApplication() accepted phase-one compatibility mode")
	}
}

func validGoAccessRuntimeConfig() config.Config {
	cfg := config.Default()
	cfg.Kafka.Brokers = []string{"127.0.0.1:9092"}
	cfg.Kafka.TriggerEvent.Topic = "alarmd-trigger-event"
	cfg.Kafka.AllowedOutputTopics = []string{"alarmd-trigger-event"}
	cfg.Kafka.ClientID = "alarmd"
	cfg.Kafka.BrokerVersion = "2.6.0"
	cfg.Redis.Address = "127.0.0.1:6379"
	cfg.Redis.StatePrefix = "alarmd-phase-two"
	return cfg
}
