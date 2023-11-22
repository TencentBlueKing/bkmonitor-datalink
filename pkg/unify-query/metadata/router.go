// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

var (
	queryRouter = &QueryRouter{}
)

type QueryRouter struct {
	ctx    context.Context
	cancel context.CancelFunc

	mtx sync.RWMutex
	wg  sync.WaitGroup

	vmQuerySpaceUid map[string]struct{}
}

func GetQueryRouter() *QueryRouter {
	return queryRouter
}

func (q *QueryRouter) MockSpaceUid(spaceUid ...string) {
	q.mtx.Lock()
	defer q.mtx.Unlock()
	if q.vmQuerySpaceUid == nil {
		q.vmQuerySpaceUid = map[string]struct{}{}
	}
	for _, uid := range spaceUid {
		q.vmQuerySpaceUid[uid] = struct{}{}
	}
}

func (q *QueryRouter) Print() string {
	q.mtx.Lock()
	defer q.mtx.Unlock()
	v, _ := json.Marshal(q.vmQuerySpaceUid)
	return fmt.Sprintf("bkmonitorv3:vm-query:space_uid\n%s", v)
}

func (q *QueryRouter) Reload(ctx context.Context) error {
	err := q.stop()
	if err != nil {
		return err
	}
	return q.start(ctx)
}

func (q *QueryRouter) PublishVmQuery(ctx context.Context) error {
	key := `bkmonitorv3:vm-query`
	msg := fmt.Sprintf(`{"time":%d}`, time.Now().Unix())
	log.Debugf(ctx, "publish %s %s", key, msg)
	return redis.Client().Publish(ctx, key, msg).Err()
}

func (q *QueryRouter) CheckVmQuery(ctx context.Context, spaceUid string) bool {
	q.mtx.RLock()
	defer q.mtx.RUnlock()
	_, ok := q.vmQuerySpaceUid[spaceUid]
	return ok
}

func (q *QueryRouter) stop() error {
	if q.cancel != nil {
		q.cancel()
	}
	q.wg.Wait()
	return nil
}

func (q *QueryRouter) start(ctx context.Context) error {
	q.mtx.Lock()
	q.ctx, q.cancel = context.WithCancel(ctx)
	q.mtx.Unlock()

	// 加载 vmQuery 查询路由信息
	err := q.loadVmQuerySpaceUid()

	// 订阅 vmQuery 查询路由的变更
	err = q.subVmQuerySpaceUid()

	return err
}

func (q *QueryRouter) subVmQuerySpaceUid() error {
	key := `bkmonitorv3:vm-query`
	ch := redis.Client().Subscribe(q.ctx, key).Channel()

	log.Debugf(q.ctx, "sub vm query space uid %s", key)

	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		for {
			select {
			case <-q.ctx.Done():
				return
			case <-ch:
				q.loadVmQuerySpaceUid()
			}
		}
	}()
	return nil
}

func (q *QueryRouter) loadVmQuerySpaceUid() error {

	key := `bkmonitorv3:vm-query:space_uid`
	spaceUid, err := redis.Client().SMembers(q.ctx, key).Result()

	log.Debugf(q.ctx, "load vm query key :%s, space uid num: %d", key, len(spaceUid))

	if err != nil {
		return err
	}

	q.mtx.Lock()
	defer q.mtx.Unlock()

	q.vmQuerySpaceUid = make(map[string]struct{}, len(spaceUid))
	for _, uid := range spaceUid {
		metric.VmQueryInfo(q.ctx, 1, uid)
		q.vmQuerySpaceUid[uid] = struct{}{}
	}
	return nil
}
