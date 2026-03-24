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
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus-operator/prometheus-operator/pkg/informers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/gzip"
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
	events   chan configFile
}

func newSecretManager(ctx context.Context, config SecretConfig, client kubernetes.Interface) *secretManager {
	ctx, cancel := context.WithCancel(ctx)
	return &secretManager{
		ctx:    ctx,
		cancel: cancel,
		client: client,
		config: config,
		files:  make(map[string]map[string][]byte),
		events: make(chan configFile, 1024),
	}
}

const (
	actionDelete         = "delete"
	actionCreateOrUpdate = "createOrUpdate"
)

type configFile struct {
	action string
	name   string
	data   []byte
}

func (sm *secretManager) Start() error {
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
	go func() {
		for range sm.events {
			// 排空
		}
	}()

	sm.cancel()
}

func (sm *secretManager) Watch() chan configFile {
	return sm.events
}

func (sm *secretManager) deleteFiles(secret *corev1.Secret) {
	sm.mut.Lock()
	defer sm.mut.Unlock()

	for filename := range sm.files[secret.Name] {
		if !strings.HasSuffix(filename, ".conf") {
			continue
		}
		sm.events <- configFile{
			action: actionDelete,
			name:   filename,
		}
	}
	delete(sm.files, secret.Name)
}

func (sm *secretManager) createOrUpdateFiles(secret *corev1.Secret) {
	sm.mut.Lock()
	defer sm.mut.Unlock()

	data := make(map[string][]byte)
	for filename, v := range secret.Data {
		// 采集配置必须是 .conf 后缀文件
		if !strings.HasSuffix(filename, ".conf") {
			continue
		}
		uncompress, err := gzip.Uncompress(v)
		if err != nil {
			logger.Errorf("gzip uncompress file (%s) failed: %v", filename, err)
			continue
		}
		data[secret.Name+"-"+filename] = uncompress
	}

	preFiles, ok := sm.files[secret.Name]
	if !ok {
		// 如果之前从未出现的 secret 则所有文件都触发事件
		for filename, content := range data {
			sm.events <- configFile{
				action: actionCreateOrUpdate,
				name:   filename,
				data:   content,
			}
		}
		sm.files[secret.Name] = data
		return
	}

	// 之前出现过 那就需要对比 add/delete 事件
	for filename, preV := range preFiles {
		newV, ok := data[filename]
		if ok {
			// 文件之前已经存在
			// 存在且内容发生变更
			if !bytes.Equal(preV, newV) {
				sm.events <- configFile{
					action: actionCreateOrUpdate,
					name:   filename,
					data:   newV,
				}
			}
		} else {
			// 文件之前已经存在 但现在不存在
			// 表明文件已经被删除
			sm.events <- configFile{
				action: actionDelete,
				name:   filename,
			}
		}
	}

	for filename, newV := range data {
		_, ok := preFiles[filename]
		if !ok {
			// 文件之前不存在 但现在存在
			// 表明为新增文件
			sm.events <- configFile{
				action: actionCreateOrUpdate,
				name:   filename,
				data:   newV,
			}
		}
	}

	sm.files[secret.Name] = data
}

func (sm *secretManager) handleSecretAdd(obj any) {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		logger.Errorf("excepted Secret type, got %T", obj)
		return
	}

	logger.Infof("manager add secret %s", secret.Name)
	sm.createOrUpdateFiles(secret)
}

func (sm *secretManager) handleSecretUpdate(oldObj any, newObj any) {
	prevSecret, ok := oldObj.(*corev1.Secret)
	if !ok {
		logger.Errorf("excepted Secret type, got %T", oldObj)
		return
	}

	currSecret, ok := newObj.(*corev1.Secret)
	if !ok {
		logger.Errorf("excepted Secret type, got %T", newObj)
		return
	}

	if prevSecret.ResourceVersion == currSecret.ResourceVersion {
		logger.Debugf("secret (%s) nothing changed, skipped", currSecret.Name)
		return
	}

	logger.Infof("manager update secret %s", currSecret.Name)
	sm.createOrUpdateFiles(currSecret)
}

func (sm *secretManager) handleSecretDelete(obj any) {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		logger.Errorf("excepted Secret type, got %T", obj)
		return
	}

	logger.Infof("manager delete secret %s", secret.Name)
	sm.deleteFiles(secret)
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

func (s *Sidecar) handleChildConfigFiles(cf configFile) error {
	path := s.realPath(cf.name)
	logger.Infof("child config (%s) triggered %s action", cf.name, cf.action)

	switch cf.action {
	case actionCreateOrUpdate:
		if utils.PathExist(path) {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.Equal(b, cf.data) {
				// 如果文件存在且内容没变更
				return nil
			}

			// 否则先删除文件
			if err := os.Remove(path); err != nil {
				return err
			}
		}
		return os.WriteFile(path, cf.data, 0o666)

	default: // actionDelete
		if !utils.PathExist(path) {
			return nil
		}
		return os.Remove(path)
	}
}
