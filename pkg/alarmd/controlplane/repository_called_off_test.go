// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"context"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A repository call its caller called off is not a failure of the store: a
// stop writes no failure line and counts nothing failed. The same call
// failing under a live context, against a store that does not answer, is
// one, and is reported as before.
func TestARepositoryCallCalledOffIsNotAFailure(t *testing.T) {
	// Nothing listens on port 1: a live context gets a refused connection.
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: time.Second})
	t.Cleanup(func() { _ = client.Close() })
	repository, err := NewRedisCatalogRepository(client, "alarmd:test:called-off", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var failed []observability.Observation
	repository.ConfigureObserver(observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		if observation.Result == observability.ResultFailed || observation.Result == observability.ResultTimeout {
			failed = append(failed, observation)
		}
	}))

	calledOff, cancel := context.WithCancel(context.Background())
	cancel()
	if err := repository.RenewCurrentActivationObjects(calledOff); err == nil {
		t.Fatal("RenewCurrentActivationObjects() under a cancelled context succeeded")
	}
	if len(failed) != 0 {
		t.Fatalf("a call its caller called off was reported failed: %+v", failed)
	}

	if err := repository.RenewCurrentActivationObjects(context.Background()); err == nil {
		t.Fatal("RenewCurrentActivationObjects() against a store that does not answer succeeded")
	}
	if len(failed) == 0 {
		t.Fatal("a call that failed under a live context was not reported failed")
	}

	// A deadline that passed is not a caller calling the work off: a read
	// that ran out of time is a failure, and is reported.
	failed = nil
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second)) // fixed-wait: a deadline already past, not a wait
	defer stop()
	if err := repository.RenewCurrentActivationObjects(expired); err == nil {
		t.Fatal("RenewCurrentActivationObjects() past its deadline succeeded")
	}
	if len(failed) == 0 {
		t.Fatal("a call that ran out of time was not reported failed")
	}
}
