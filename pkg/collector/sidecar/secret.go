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
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus-operator/prometheus-operator/pkg/informers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type secretManager struct {
	ctx    context.Context
	cancel context.CancelFunc
	client kubernetes.Interface

	config SecretConfig

	mut   sync.RWMutex
	files map[string]map[string][]byte // map[secretName]map[configFile]data

	secrInfs *informers.ForResource
}

func (sm *secretManager) Run() error {
	var err error
	sm.secrInfs, err = newSecretInformer(sm.client, sm.config)
	if err != nil {
		return err
	}

	sm.secrInfs.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    sm.handleSecretAdd,
		DeleteFunc: sm.handleSecretDelete,
		UpdateFunc: sm.handleSecretUpdate,
	})

	sm.secrInfs.Start(sm.ctx.Done())
	for _, inf := range sm.secrInfs.GetInformers() {
		if !k8sutils.WaitForNamedCacheSync(sm.ctx, "Secret", inf.Informer()) {
			return errors.New("failed to sync Secret caches")
		}
	}

	return nil
}

func (sm *secretManager) Stop() {
	sm.cancel()
}

func (sm *secretManager) Signal() chan struct{} {
	return nil
}

func (sm *secretManager) createOrUpdateFiles(secret *corev1.Secret) bool {
	sm.mut.Lock()
	defer sm.mut.Unlock()

	added := make(map[string]struct{})
	data := make(map[string][]byte)
	for k, v := range secret.Data {
		if !strings.HasSuffix(k, ".conf") {
			continue
		}
		data[k] = v
		added[k] = struct{}{}
	}

	diff := !reflect.DeepEqual(data, sm.files[secret.Name])
	sm.files[secret.Name] = data
	return diff
}

func (sm *secretManager) handleSecretAdd(obj interface{}) {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		logger.Errorf("excepted Secret type, got %T", obj)
		return
	}

	sm.createOrUpdateFiles(secret)
}

func (sm *secretManager) handleSecretDelete(obj interface{}) {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		logger.Errorf("excepted Secret type, got %T", obj)
		return
	}

	sm.mut.Lock()
	defer sm.mut.Unlock()

	delete(sm.files, secret.Name)
}

func (sm *secretManager) handleSecretUpdate(_ interface{}, obj interface{}) {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		logger.Errorf("excepted Secret type, got %T", obj)
		return
	}

	sm.mut.Lock()
	defer sm.mut.Unlock()

	delete(sm.files, secret.Name)
	sm.createOrUpdateFiles(secret)
}

func newSecretInformer(client kubernetes.Interface, secretConfig SecretConfig) (*informers.ForResource, error) {
	informer, err := informers.NewInformersForResource(
		informers.NewKubeInformerFactories(
			map[string]struct{}{secretConfig.Namespace: {}},
			nil,
			client,
			5*time.Minute,
			func(options *metav1.ListOptions) {
				options.LabelSelector = secretConfig.Selector
			},
		),
		corev1.SchemeGroupVersion.WithResource(string(corev1.ResourceSecrets)),
	)
	if err != nil {
		return nil, errors.Wrap(err, "create secret informer failed")
	}
	return informer, nil
}
