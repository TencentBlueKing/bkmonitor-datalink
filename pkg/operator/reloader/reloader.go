// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package reloader

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus-operator/prometheus-operator/pkg/informers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/action"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/filewatcher"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/notifier"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/reloader/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/gzip"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Reloader struct {
	ctx      context.Context
	cancel   context.CancelFunc
	client   kubernetes.Interface
	wg       sync.WaitGroup
	secrInfs *informers.ForResource
	bus      *notifier.RateBus
}

func New(ctx context.Context) (*Reloader, error) {
	if err := os.Setenv("KUBECONFIG", configs.G().KubeConfig); err != nil {
		return nil, err
	}
	client, err := k8sutils.NewK8SClientInsecure()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	reloader := &Reloader{
		ctx:    ctx,
		cancel: cancel,
		client: client,
		bus:    notifier.NewDefaultRateBus(),
	}

	return reloader, nil
}

func newSecretInformer(client kubernetes.Interface, labelSelector string) (*informers.ForResource, error) {
	informer, err := informers.NewInformersForResource(
		informers.NewKubeInformerFactories(
			map[string]struct{}{configs.G().MetaEnv.Namespace: {}},
			nil,
			client,
			define.ReSyncPeriod,
			func(options *metav1.ListOptions) {
				options.LabelSelector = labelSelector
			},
		),
		corev1.SchemeGroupVersion.WithResource(string(corev1.ResourceSecrets)),
	)
	if err != nil {
		return nil, errors.Wrap(err, "create secret informer failed")
	}
	return informer, nil
}

func (r *Reloader) Run() error {
	// 启动前校验 taskType 是否合法
	if !tasks.ValidateTaskType(configs.G().TaskType) {
		return fmt.Errorf("invalid task type '%s'", configs.G().TaskType)
	}

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.loopHandle()
	}()

	var err error
	r.secrInfs, err = newSecretInformer(r.client, tasks.GetTaskLabelSelector(configs.G().TaskType))
	if err != nil {
		return err
	}

	r.secrInfs.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    r.handleSecretAdd,
		DeleteFunc: r.handleSecretDelete,
		UpdateFunc: r.handleSecretUpdate,
	})

	r.secrInfs.Start(r.ctx.Done())
	for _, inf := range r.secrInfs.GetInformers() {
		if !k8sutils.WaitForNamedCacheSync(r.ctx, "Secret", inf.Informer()) {
			return errors.New("failed to sync Secret caches")
		}
	}

	// 启动文件监听
	for _, path := range configs.G().WatchPath {
		ch, err := filewatcher.AddPath(path)
		if err != nil {
			return err
		}
		r.loopWatchFile(path, ch)
	}

	return nil
}

func (r *Reloader) loopWatchFile(path string, ch <-chan struct{}) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		for {
			select {
			case <-r.ctx.Done():
				if err := filewatcher.RemovePath(path); err != nil {
					logger.Errorf("remove path (%s) failed: %s", path, err)
				}
				return

			case <-ch:
				r.bus.Publish()
			}
		}
	}()
}

func (r *Reloader) Stop() {
	r.cancel()
	r.wg.Wait()
}

func (r *Reloader) handleSecretAdd(obj any) {
	r.handleSecrets(action.Add, obj)
}

func (r *Reloader) handleSecretDelete(obj any) {
	r.handleSecrets(action.Delete, obj)
}

func (r *Reloader) handleSecretUpdate(_ any, newObj any) {
	r.handleSecrets(action.Update, newObj)
}

func (r *Reloader) handleSecrets(act string, obj any) {
	var secretName string
	switch configs.G().TaskType {
	case tasks.TaskTypeDaemonSet:
		secretName = tasks.GetDaemonSetTaskSecretName(configs.G().MetaEnv.NodeName)

	case tasks.TaskTypeStatefulSet:
		// bkm-statefulset-worker-0 => [0]
		// bkm-statefulset-worker-1 => [1]
		part := strings.Split(configs.G().MetaEnv.PodName, "-")
		if len(part) > 0 {
			i, err := strconv.Atoi(part[len(part)-1])
			if err == nil {
				secretName = tasks.GetStatefulSetTaskSecretName(i)
			}
		}
	case tasks.TaskTypeEvent:
		secretName = tasks.GetEventTaskSecretName()
	}

	secret, ok := obj.(*corev1.Secret)
	if !ok {
		logger.Errorf("excepted Secret type, got %T", obj)
		return
	}

	if secret.Name != secretName {
		logger.Debugf("found secret '%s', but skip", secret.Name)
		return
	}

	logger.Infof("secret '%s' triggered %s action", secret.Name, act)

	// 删除事件则置空文件
	if act == action.Delete {
		secret = secret.DeepCopy()
		secret.Data = make(map[string][]byte)
	}

	if err := r.syncSecretToFiles(secret); err != nil {
		logger.Errorf("failed to sync secret '%s' to files: %v", secret.Name, err)
	}
}

func (r *Reloader) syncSecretToFiles(secret *corev1.Secret) error {
	files := make(map[string][]byte)
	for fileName, data := range secret.Data {
		files[fileName] = data
	}

	var changed bool
	set := make(map[string]struct{})
	for filename, data := range files {
		filePath := filepath.Join(configs.G().ChildConfigPath, filename)

		// 如果存在无法解压缩的数据则直接使用原始数据
		uncompressed, err := gzip.Uncompress(data)
		if err != nil {
			logger.Errorf("failed to uncompress config (%s): %v", filePath, err)
			continue
		}

		ok, err := writeTo(uncompressed, filePath)
		if err != nil {
			logger.Errorf("write file '%s' failed: %s", filename, err)
			continue
		}

		if ok {
			changed = true
			logger.Infof("write file '%s'", filePath)
		}
		set[filename] = struct{}{}
	}

	// 遍历目标文件夹，删除 secret 中不存在的目标文件
	var deleted bool
	dirs, err := os.ReadDir(configs.G().ChildConfigPath)
	if err != nil {
		return err
	}

	for _, f := range dirs {
		if _, ok := set[f.Name()]; !ok {
			filePath := filepath.Join(configs.G().ChildConfigPath, f.Name())
			logger.Infof("remove file '%s'", filePath)
			if err := os.Remove(filePath); err != nil {
				logger.Errorf("failed to remove file '%s': %v", filePath, err)
			}
			deleted = true
		}
	}

	if !changed && !deleted {
		return nil
	}

	r.bus.Publish()
	return nil
}

func (r *Reloader) loopHandle() {
	ticker := time.NewTicker(30 * time.Minute) // 避免出现非预期的事件丢失而导致不触发 reload 操作
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return

		case <-r.bus.Subscribe():
			if err := r.sendReloadSignal(); err != nil {
				logger.Errorf("[bus] failed to send reload signal: %s", err)
			}

		case <-ticker.C:
			if err := r.sendReloadSignal(); err != nil {
				logger.Errorf("[ticker] failed to send reload signal: %s", err)
			}
		}
	}
}

func (r *Reloader) sendReloadSignal() error {
	content, err := os.ReadFile(configs.G().PidPath)
	if err != nil {
		return err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil {
		return errors.Wrapf(err, "invalid pid content(%s)", string(content))
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return errors.Wrap(err, "find process failed")
	}

	// 发送 reload 信号
	if err = process.Signal(syscall.SIGUSR1); err != nil {
		return errors.Wrap(err, "publish signal failed")
	}
	logger.Infof("reload process(%d) finished", pid)
	return nil
}

// writeTo 数据写入 dst 文件中
// 返回的 bool 表示是否有真实的数据发生变动(可能只是触发了刷新但所有配置文件都没变化)
func writeTo(b []byte, dst string) (bool, error) {
	content, err := os.ReadFile(dst)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, err
		}
		if err = os.WriteFile(dst, b, 0o644); err != nil {
			return false, err
		}
		return true, nil
	}

	// 内容一致则不改动
	if string(b) == string(content) {
		return false, nil
	}

	// 否则覆盖到 target
	if err = os.WriteFile(dst, b, 0o644); err != nil {
		return false, err
	}
	return true, nil
}
