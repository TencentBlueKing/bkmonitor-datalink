// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sidecar

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/tools/cache"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	bkv1beta1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/apis/crd/v1beta1"
	bkversioned "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/client/clientset/versioned"
	bkinformers "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/client/informers/externalversions"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	keyUsage    = "usage"
	keyTokenRef = "tokenRef"
	keyScope    = "scope"

	usagePrefix = "collector"
)

type IDSpec struct {
	Token  string
	Type   string // traces/metrics/logs/profiles (required)
	Scope  string // privileged/application (required)
	DataID int
}

// Watcher 负责监听和处理 DataID 资源
// 目前仅负责处理 collector 自己归属的 dataid
type Watcher struct {
	ctx      context.Context
	cancel   context.CancelFunc
	informer cache.SharedIndexInformer

	mut     sync.Mutex
	dataids map[string]IDSpec
}

func newWatcher(ctx context.Context, client bkversioned.Interface) *Watcher {
	ctx, cancel := context.WithCancel(ctx)
	factory := bkinformers.NewSharedInformerFactory(client, 5*time.Minute)

	return &Watcher{
		ctx:      ctx,
		cancel:   cancel,
		dataids:  make(map[string]IDSpec),
		informer: factory.Monitoring().V1beta1().DataIDs().Informer(),
	}
}

func (w *Watcher) Start() error {
	w.informer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    w.handleDataIDAdd,
			DeleteFunc: w.handleDataIDDelete,
			UpdateFunc: w.handleDataIDUpdate,
		},
	)
	go w.informer.Run(w.ctx.Done())

	if !k8sutils.WaitForNamedCacheSync(w.ctx, "DataID", w.informer) {
		return errors.New("failed to sync DataID caches")
	}

	return nil
}

func (w *Watcher) Stop() {
	w.cancel()
}

func (w *Watcher) DataIDs() []IDSpec {
	w.mut.Lock()
	defer w.mut.Unlock()

	var ret []IDSpec
	for _, v := range w.dataids {
		ret = append(ret, v)
	}

	sort.Slice(ret, func(i, j int) bool {
		return ret[i].DataID < ret[j].DataID
	})
	return ret
}

func (w *Watcher) upsertDataID(dataID *bkv1beta1.DataID) {
	w.mut.Lock()
	defer w.mut.Unlock()

	// 只处理 collector 用途且 privileged scope 的 dataid
	usage := dataID.Labels[keyUsage]
	if !strings.HasPrefix(usage, usagePrefix) {
		logger.Warnf("want collector dataid, but go '%s', skipped", usage)
		return
	}
	scope := dataID.Labels[keyScope]
	if scope != define.ConfigTypePrivileged {
		logger.Warnf("want privileged scope, but go '%s'", scope)
		return
	}

	// collector.traces
	// collector.metrics
	// ...
	parts := strings.Split(usage, ".")
	if len(parts) != 2 {
		logger.Warnf("invalid usage format '%s'", usage)
		return
	}
	switch parts[1] {
	case define.RecordTraces.S(), define.RecordMetrics.S(), define.RecordLogs.S(), define.RecordProfiles.S():
	default:
		logger.Warnf("unsupported dataid type '%s'", parts[1])
		return
	}

	token := dataID.Labels[keyTokenRef]
	uid := fmt.Sprintf("%s/%d", token, dataID.Spec.DataID)
	w.dataids[uid] = IDSpec{
		Token:  token,
		Type:   parts[1],
		DataID: dataID.Spec.DataID,
		Scope:  scope,
	}
	logger.Infof("handle dataid: %+v", w.dataids[uid])
}

func (w *Watcher) deleteDataID(dataID *bkv1beta1.DataID) {
	w.mut.Lock()
	defer w.mut.Unlock()

	uid := fmt.Sprintf("%s/%d", dataID.Labels[keyTokenRef], dataID.Spec.DataID)

	v, ok := w.dataids[uid]
	if ok {
		logger.Infof("remove dataid: %+v", v)
	}
	delete(w.dataids, uid)
}

func (w *Watcher) handleDataIDAdd(obj interface{}) {
	dataID, ok := obj.(*bkv1beta1.DataID)
	if !ok {
		logger.Errorf("unexpected DataID type, got %T", obj)
		return
	}
	w.upsertDataID(dataID)
}

func (w *Watcher) handleDataIDDelete(obj interface{}) {
	dataID, ok := obj.(*bkv1beta1.DataID)
	if !ok {
		logger.Errorf("unexpected DataID type, got %T", obj)
		return
	}
	w.deleteDataID(dataID)
}

func (w *Watcher) handleDataIDUpdate(oldObj interface{}, newObj interface{}) {
	w.handleDataIDDelete(oldObj) // 辞旧
	w.handleDataIDAdd(newObj)    // 迎新
}
