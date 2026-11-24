// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package eventsource

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"linkd/internal/config"
)

func TestSubscriptionUpdatePublishesNewReleaseWithoutChangingHistory(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*config.KafkaStorageConfig)
	}{
		{"topic", func(k *config.KafkaStorageConfig) { k.Topic = "alarmd_event" }},
		{"consumer group", func(k *config.KafkaStorageConfig) { k.ConsumerGroup = "new-consumer" }},
		{"brokers", func(k *config.KafkaStorageConfig) { k.Brokers = []string{"new-kafka:9092"} }},
		{"complete subscription", func(k *config.KafkaStorageConfig) {
			k.Topic, k.ConsumerGroup, k.Brokers = "alarmd_event", "new-consumer", []string{"new-kafka:9092"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(newDocs(), config.DefaultSeverityConfig())
			first, err := s.Apply(t.Context(), sample(), 0, false, "create")
			if err != nil {
				t.Fatal(err)
			}
			before, err := s.GetRelease(t.Context(), first.ID, first.Published)
			if err != nil {
				t.Fatal(err)
			}
			next := sample()
			tc.edit(&next.Storage.Kafka)
			for _, options := range [][]ApplyOptions{nil, {{AllowSubscriptionChange: false}}} {
				if _, err := s.Apply(t.Context(), next, first.Revision, false, "ordinary", options...); err == nil || !strings.Contains(err.Error(), "allow_subscription_change=true") {
					t.Fatalf("ordinary subscription edit accepted: %v", err)
				}
			}
			changed, err := s.Apply(t.Context(), next, first.Revision, false, "update", ApplyOptions{AllowSubscriptionChange: true})
			if err != nil {
				t.Fatal(err)
			}
			if changed.Revision != 2 || changed.Published != 2 || changed.Pending != nil || !changed.Spec.Enabled {
				t.Fatalf("subscription not published: %+v", changed)
			}
			old, err := s.GetRelease(t.Context(), first.ID, first.Published)
			if err != nil || !reflect.DeepEqual(old, before) {
				t.Fatalf("historical release changed: %v", err)
			}
			current, err := s.GetRelease(t.Context(), first.ID, changed.Published)
			if err != nil || !reflect.DeepEqual(current.Spec.Storage.Kafka, next.Storage.Kafka) {
				t.Fatalf("new subscription was not frozen: %v", err)
			}
			retry, err := s.Apply(t.Context(), next, first.Revision, false, "retry")
			if err != nil || retry.Revision != changed.Revision {
				t.Fatalf("retry republished: %v", err)
			}
			stale := sample()
			stale.Storage.Kafka.Topic = "stale-topic"
			if _, err := s.Apply(t.Context(), stale, first.Revision, false, "stale"); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale edit accepted: %v", err)
			}
			if _, err := s.Apply(t.Context(), stale, changed.Revision, false, "later ordinary"); err == nil || !strings.Contains(err.Error(), "allow_subscription_change=true") {
				t.Fatalf("maintenance permission leaked to later edit: %v", err)
			}
		})
	}
}

func TestSubscriptionEditDoesNotAllowTenantOrFingerprintMigration(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*config.EventSource)
	}{
		{"tenant", func(s *config.EventSource) { s.RelatedTenantID = "another-tenant" }},
		{"fingerprint", func(s *config.EventSource) { s.FingerprintMode, s.FingerprintField = "field", "subject_id" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(newDocs(), config.DefaultSeverityConfig())
			first, err := s.Apply(t.Context(), sample(), 0, false, "create")
			if err != nil {
				t.Fatal(err)
			}
			next := sample()
			next.Storage.Kafka.Topic = "alarmd_event"
			tc.edit(&next)
			if _, err := s.Apply(t.Context(), next, first.Revision, false, "invalid", ApplyOptions{AllowSubscriptionChange: true}); err == nil || !strings.Contains(err.Error(), "migration is not supported") {
				t.Fatalf("identity migration accepted: %v", err)
			}
			current, err := s.Get(t.Context(), first.ID)
			if err != nil || current.Revision != first.Revision {
				t.Fatalf("rejected edit changed record: %v", err)
			}
		})
	}
}
