// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"testing"

	"linkd/internal/config"
	"linkd/internal/eventsource"
)

type closeProducer struct {
	recordingProducer
	closed int
}

func (p *closeProducer) Close() { p.closed++ }

func TestFactoryRetiresAfterInflightPublication(t *testing.T) {
	factory := NewFactory(nil, nil)
	var opened []*closeProducer
	factory.open = func(config.KafkaStorageConfig) (ownedProducer, error) {
		p := &closeProducer{}
		opened = append(opened, p)
		return p, nil
	}
	release := eventsource.Release{ID: "source", Version: 1, Spec: config.EventSource{RelatedTenantID: "tenant"}}
	first, err := factory.acquire(release)
	if err != nil {
		t.Fatal(err)
	}
	same, err := factory.acquire(release)
	if err != nil || same != first || len(opened) != 1 {
		t.Fatal("connection not reused")
	}
	release.Version = 2
	next, err := factory.acquire(release)
	if err != nil {
		t.Fatal(err)
	}
	if opened[0].closed != 0 {
		t.Fatal("inflight producer closed")
	}
	factory.release(first)
	if opened[0].closed != 0 {
		t.Fatal("second inflight publisher lost connection")
	}
	factory.release(same)
	if opened[0].closed != 1 {
		t.Fatal("retired producer not closed")
	}
	factory.Close()
	if opened[1].closed != 0 {
		t.Fatal("shutdown interrupted active publisher")
	}
	factory.release(next)
	if opened[1].closed != 1 {
		t.Fatal("shutdown did not release producer")
	}
	factory.Close()
	if _, err = factory.acquire(release); err == nil {
		t.Fatal("closed factory accepted new publisher")
	}
}
