// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package qcloudmonitor

import (
	"context"
	"reflect"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Syncer interface {
	Sync(ctx context.Context, namespace, name string) error
}

type syncEventHandler struct {
	syncer     Syncer
	reconcileQ workqueue.TypedRateLimitingInterface[string]

	g errgroup.Group
}

var _ = cache.ResourceEventHandler(&syncEventHandler{})

func newSyncEventHandler(syncer Syncer) *syncEventHandler {
	return &syncEventHandler{
		syncer: syncer,
		reconcileQ: workqueue.NewTypedRateLimitingQueueWithConfig[string](
			workqueue.NewTypedMaxOfRateLimiter(
				workqueue.NewTypedItemExponentialFailureRateLimiter[string](5*time.Millisecond, 1000*time.Second),
				&workqueue.TypedBucketRateLimiter[string]{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
			),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: "sync",
			},
		),
	}
}

func (r *syncEventHandler) objectKey(obj any) (string, bool) {
	k, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		return "", false
	}

	// <Namespace>/<Name>
	if !strings.Contains(k, "/") {
		return "", false
	}
	return k, true
}

func (r *syncEventHandler) deletionInProgress(o metav1.Object) bool {
	return o.GetDeletionTimestamp() != nil
}

func (r *syncEventHandler) hasObjectChanged(old, cur metav1.Object) bool {
	if old.GetGeneration() != cur.GetGeneration() {
		return true
	}
	if !reflect.DeepEqual(old.GetLabels(), cur.GetLabels()) {
		return true
	}
	if !reflect.DeepEqual(old.GetAnnotations(), cur.GetAnnotations()) {
		return true
	}
	if old.GetResourceVersion() != cur.GetResourceVersion() {
		return true
	}
	return false
}

func (r *syncEventHandler) OnAdd(obj any, _ bool) {
	key, ok := r.objectKey(obj)
	if !ok {
		return
	}

	_, err := meta.Accessor(obj)
	if err != nil {
		return
	}

	logger.Infof("handle (Add) event, name=%s", key)
	r.reconcileQ.Add(key)
}

func (r *syncEventHandler) OnUpdate(old, cur any) {
	key, ok := r.objectKey(cur)
	if !ok {
		return
	}

	mOld, err := meta.Accessor(old)
	if err != nil {
		logger.Errorf("failed to get old object meta, key=%s: %v", key, err)
		return
	}
	mCur, err := meta.Accessor(cur)
	if err != nil {
		logger.Errorf("failed to get current object meta, key=%s: %v", key, err)
		return
	}

	if r.deletionInProgress(mCur) {
		return
	}
	if !r.hasObjectChanged(mOld, mCur) {
		return
	}

	logger.Infof("handle (Update) event, name=%s", key)
	r.reconcileQ.Add(key)
}

func (r *syncEventHandler) OnDelete(obj any) {
	key, ok := r.objectKey(obj)
	if !ok {
		return
	}

	_, err := meta.Accessor(obj)
	if err != nil {
		return
	}

	logger.Infof("handle (Delete) event, name=%s", key)
	r.reconcileQ.Add(key)
}

func (r *syncEventHandler) Run(ctx context.Context) {
	r.g.Go(func() error {
		for r.processNextReconcileItem(ctx) {
		}
		return nil
	})
}

func (r *syncEventHandler) Stop() {
	r.reconcileQ.ShutDown()
	_ = r.g.Wait()
}

func (r *syncEventHandler) processNextReconcileItem(ctx context.Context) bool {
	key, quit := r.reconcileQ.Get()
	if quit {
		return false
	}
	defer r.reconcileQ.Done(key)

	objName, _ := cache.ParseObjectName(key)
	err := r.syncer.Sync(ctx, objName.Namespace, objName.Name)
	if err == nil {
		r.reconcileQ.Forget(key)
		return true
	}

	// defaultMetricMonitor.IncReconcileQCloudMonitorFailedCounter(key)
	logger.Errorf("failed to reconcile object, key=%s: %v", key, err)
	r.reconcileQ.AddRateLimited(key)
	return true
}
