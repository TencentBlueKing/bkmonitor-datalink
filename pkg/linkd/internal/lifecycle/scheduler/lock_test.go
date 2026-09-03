// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
)

type fakeRedisLeaseClient struct {
	setValue bool
	setErr   error
	evalVals []int64
	evalErrs []error
	setKey   string
	setToken string
	setTTL   time.Duration
	evalKeys []string
}

func (c *fakeRedisLeaseClient) SetNX(
	ctx context.Context,
	key string,
	value any,
	expiration time.Duration,
) *redis.BoolCmd {
	c.setKey = key
	c.setToken, _ = value.(string)
	c.setTTL = expiration
	command := redis.NewBoolCmd(ctx)
	command.SetVal(c.setValue)
	command.SetErr(c.setErr)
	return command
}

func (c *fakeRedisLeaseClient) Eval(
	ctx context.Context,
	_ string,
	keys []string,
	_ ...any,
) *redis.Cmd {
	c.evalKeys = append(c.evalKeys, keys...)
	command := redis.NewCmd(ctx)
	if len(c.evalVals) > 0 {
		command.SetVal(c.evalVals[0])
		c.evalVals = c.evalVals[1:]
	}
	if len(c.evalErrs) > 0 {
		command.SetErr(c.evalErrs[0])
		c.evalErrs = c.evalErrs[1:]
	}
	return command
}

func TestRedisLockerAcquireRenewRelease(t *testing.T) {
	t.Parallel()
	client := &fakeRedisLeaseClient{setValue: true, evalVals: []int64{1, 1}}
	config := DefaultConfig()
	locker := newRedisLocker(client, config, func() (string, error) { return "token-1", nil })
	lease, err := locker.Acquire(context.Background(), "order-1")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if client.setKey != config.LockKeyPrefix+":order-1" || client.setToken != "token-1" ||
		client.setTTL != config.LockTTL {
		t.Fatalf("Acquire() set key=%q token=%q ttl=%s", client.setKey, client.setToken, client.setTTL)
	}
	if err := locker.Renew(context.Background(), lease); err != nil {
		t.Fatalf("Renew() error = %v", err)
	}
	if err := locker.Release(context.Background(), lease); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

func TestRedisLockerBusyAndLostLease(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()
	busy := newRedisLocker(
		&fakeRedisLeaseClient{setValue: false},
		config,
		func() (string, error) { return "token", nil },
	)
	if _, err := busy.Acquire(context.Background(), "order-1"); !errors.Is(err, ErrLockBusy) {
		t.Fatalf("Acquire() error = %v, want ErrLockBusy", err)
	}
	lost := newRedisLocker(
		&fakeRedisLeaseClient{setValue: true, evalVals: []int64{0, 0}},
		config,
		func() (string, error) { return "token", nil },
	)
	lease, err := lost.Acquire(context.Background(), "order-1")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := lost.Renew(context.Background(), lease); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("Renew() error = %v, want ErrLeaseLost", err)
	}
	if err := lost.Release(context.Background(), lease); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("Release() error = %v, want ErrLeaseLost", err)
	}
}

func TestSchedulerConfigValidation(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()
	if err := config.Validate(); err != nil {
		t.Fatalf("DefaultConfig().Validate() error = %v", err)
	}
	config.RenewInterval = config.LockTTL / 2
	if err := config.Validate(); err == nil {
		t.Fatal("Validate() accepted unsafe renew interval")
	}
}

func TestRedisLockerTokenFailure(t *testing.T) {
	t.Parallel()
	want := errors.New("entropy unavailable")
	locker := newRedisLocker(
		&fakeRedisLeaseClient{},
		DefaultConfig(),
		func() (string, error) { return "", want },
	)
	if _, err := locker.Acquire(context.Background(), "order-1"); !errors.Is(err, want) {
		t.Fatalf("Acquire() error = %v, want token error", err)
	}
}
