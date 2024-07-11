// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package operator

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/compressor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/kits"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/target"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// 每 2 个小时全量更新一次
const resyncPeriod = time.Hour * 2

var (
	daemonsetAlarmer   = kits.NewAlarmer(resyncPeriod)
	statefulsetAlarmer = kits.NewAlarmer(resyncPeriod)
)

func Slowdown() {
	time.Sleep(time.Millisecond * 20)
}

func EqualMap(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func (c *Operator) checkStatefulSetMatchRules(childConfig *discover.ChildConfig) bool {
	meta := childConfig.Meta
	var matched bool
	for _, rule := range ConfStatefulSetMatchRules {
		// Kind/Namespace 为必选项
		if strings.ToLower(rule.Kind) == strings.ToLower(meta.Kind) && rule.Namespace == meta.Namespace {
			// 1) 如果 rule 中 name 为空表示命中所有的 resource
			// 2) 如果 rule 中 name 不为空则要求精准匹配
			if rule.Name == "" || rule.Name == meta.Name {
				matched = true
				break
			}
		}
	}
	return matched
}

func (c *Operator) createOrUpdateChildSecret(statefulset, daemonset []*discover.ChildConfig) {
	// event task 无需清理机制
	c.createOrUpdateEventTaskSecrets()

	// 不启用 daemonset worker 则将所有的任务都分配到 statefulset worker
	if !ConfEnableDaemonSetWorker {
		merged := make([]*discover.ChildConfig, 0, len(statefulset)+len(daemonset))
		merged = append(merged, statefulset...)
		merged = append(merged, daemonset...)
		c.createOrUpdateStatefulSetTaskSecrets(merged)
		c.cleanupStatefulSetChildSecret()
		c.cleanupDaemonSetChildSecret(nil) // 清理 daemonset 配置 空配置代表删除
		return
	}

	// 启用 StatefulSet Match Rules
	if len(ConfStatefulSetMatchRules) > 0 {
		tmpStatefulset := make([]*discover.ChildConfig, 0)
		tmpStatefulset = append(tmpStatefulset, statefulset...)

		deletedIdx := make(map[int]struct{})
		for idx, childConf := range daemonset {
			if c.checkStatefulSetMatchRules(childConf) {
				tmpStatefulset = append(tmpStatefulset, childConf)
				deletedIdx[idx] = struct{}{}
			}
		}

		tmpDaemonset := make([]*discover.ChildConfig, 0)
		for idx, childConf := range daemonset {
			if _, ok := deletedIdx[idx]; ok {
				continue
			}
			tmpDaemonset = append(tmpDaemonset, childConf)
		}

		// 更新变量
		statefulset = tmpStatefulset
		daemonset = tmpDaemonset
	}

	c.createOrUpdateStatefulSetTaskSecrets(statefulset)
	c.cleanupStatefulSetChildSecret()

	c.createOrUpdateDaemonSetTaskSecrets(daemonset)
	c.cleanupDaemonSetChildSecret(daemonset)
}

func (c *Operator) createOrUpdateEventTaskSecrets() {
	dataID, err := c.dw.MatchEventDataID(define.MonitorMeta{}, true)
	if err != nil {
		logger.Errorf("no event dataid found, err: %s", err)
		return
	}
	secretClient := c.client.CoreV1().Secrets(ConfMonitorNamespace)

	eventTarget := &target.EventTarget{
		DataID: dataID.Spec.DataID,
		Labels: dataID.Spec.Labels,
	}

	b, err := eventTarget.YamlBytes()
	if err != nil {
		logger.Errorf("failed to crate event target: %v", err)
		return
	}

	secretName := tasks.GetEventTaskSecretName()
	if string(b) == c.eventTaskCache {
		logger.Debug("event task nothing changed, skipped")
		return
	}
	c.eventTaskCache = string(b)

	secret := newSecret(secretName, tasks.TaskTypeEvent)
	compressed, err := compressor.Compress(b)
	if err != nil {
		logger.Errorf("failed to compress config content, err: %v", err)
		return
	}

	secret.Data[eventTarget.FileName()] = compressed
	c.mm.SetActiveSecretFileCount(tasks.TaskTypeEvent, secret.Name, 1)
	logger.Infof("event secret %s add file %s", secret.Name, eventTarget.FileName())

	if err = k8sutils.CreateOrUpdateSecret(c.ctx, secretClient, secret); err != nil {
		c.mm.IncHandledSecretFailedCounter(secret.Name, define.ActionCreateOrUpdate)
		logger.Errorf("failed to create or update event secret %s, err: %v", secret.Name, err)
		return
	}

	c.mm.IncHandledSecretSuccessCounter(secret.Name, define.ActionCreateOrUpdate)
	logger.Infof("create or update event secret %s", secret.Name)
}

// createOrUpdateDaemonSetTaskSecrets 创建 daemonset secrets
// damonset worker 将内部采集按照 node 划分调度到集群的节点中
func (c *Operator) createOrUpdateDaemonSetTaskSecrets(childConfigs []*discover.ChildConfig) {
	nodeMap := make(map[string][]*discover.ChildConfig)
	currTasksCache := make(map[string]map[string]struct{})
	for _, cfg := range childConfigs {
		if _, found := currTasksCache[cfg.Node]; !found {
			currTasksCache[cfg.Node] = map[string]struct{}{}
		}
		currTasksCache[cfg.Node][cfg.FileName] = struct{}{}

		if _, ok := nodeMap[cfg.Node]; ok {
			nodeMap[cfg.Node] = append(nodeMap[cfg.Node], cfg)
			continue
		}
		nodeMap[cfg.Node] = []*discover.ChildConfig{cfg}
	}

	maxSecretsAllowed := int(float64(c.objectsController.NodeCount()) * ConfMaxNodeSecretRatio)
	count := 0

	if daemonsetAlarmer.Alarm() {
		c.daemonSetTaskCache = map[string]map[string]struct{}{}
		logger.Info("daemonset worker resynced")
	}

	secretClient := c.client.CoreV1().Secrets(ConfMonitorNamespace)
	for node, configs := range nodeMap {
		Slowdown()
		secretName := tasks.GetDaemonSetTaskSecretName(node)
		cache := c.daemonSetTaskCache[node]
		if len(cache) > 0 && EqualMap(currTasksCache[node], cache) {
			logger.Infof("node (%s) secrets nothing changed, skipped", node)
			continue
		}

		// 确保 secrets 不会超限
		count++
		if count > maxSecretsAllowed {
			c.mm.IncSecretsExceededCounter()
			logger.Errorf("daemonset secrets exceeded, maxAllowed=%d, current=%d", maxSecretsAllowed, count)
			continue
		}

		bytesTotal := 0
		secret := newSecret(secretName, tasks.TaskTypeDaemonSet)
		for _, config := range configs {
			compressed, err := compressor.Compress(config.Data)
			if err != nil {
				logger.Errorf("failed to compress config content, addr=%s, err: %v", config.Address, err)
				continue
			}

			bytesTotal += len(compressed)
			secret.Data[config.FileName] = compressed
			logger.Debugf("daemonset secret %s add file %s", secret.Name, config.FileName)
		}

		logger.Infof("daemonset secret %s contains %d files", secret.Name, len(secret.Data))
		c.mm.SetActiveSecretFileCount(tasks.TaskTypeDaemonSet, secret.Name, len(secret.Data))

		if err := k8sutils.CreateOrUpdateSecret(c.ctx, secretClient, secret); err != nil {
			c.mm.IncHandledSecretFailedCounter(secret.Name, define.ActionCreateOrUpdate)
			delete(currTasksCache, node)
			logger.Errorf("failed to create or update secret: %v", err)
			continue
		}
		c.mm.IncHandledSecretSuccessCounter(secret.Name, define.ActionCreateOrUpdate)
		logger.Infof("create or update daemonset secret %s", secret.Name)
	}
	c.daemonSetTaskCache = currTasksCache
}

// cleanupDaemonSetChildSecret 清理 daemonset secrets
// 传入的 childConfigs 为全量子配置 这里需要判断是否有节点已经没有采集任务 如若发现 则将该节点对应的 secret 删除
func (c *Operator) cleanupDaemonSetChildSecret(childConfigs []*discover.ChildConfig) {
	foundNodeNames := make(map[string]struct{})
	for _, cfg := range childConfigs {
		foundNodeNames[cfg.Node] = struct{}{}
	}

	nodes := c.objectsController.NodeNames()

	var noConfigNodes []string
	for _, node := range nodes {
		_, ok := foundNodeNames[node]
		if !ok {
			noConfigNodes = append(noConfigNodes, node)
		}
	}

	onSuccess := true
	secretClient := c.client.CoreV1().Secrets(ConfMonitorNamespace)

	secrets, err := secretClient.List(c.ctx, metav1.ListOptions{})
	if err != nil {
		logger.Errorf("failed to list secret, error: %v", err)
		onSuccess = false
	}

	// 记录已经存在的 secrets
	existSecrets := make(map[string]struct{})
	for _, secret := range secrets.Items {
		existSecrets[secret.Name] = struct{}{}
	}
	logger.Infof("list %d secrets from %s namespace", len(existSecrets), ConfMonitorNamespace)

	dropSecrets := make(map[string]struct{})

	// 如果 node 已经没有采集配置了 则需要删除
	for _, node := range noConfigNodes {
		secretName := tasks.GetDaemonSetTaskSecretName(node)
		if _, ok := existSecrets[secretName]; !ok && onSuccess {
			continue
		}
		dropSecrets[secretName] = struct{}{}
	}

	// 如果 node 已经不存在了 也需要删除采集配置
	for secret := range existSecrets {
		// 只处理 daemonset secrets
		if !strings.HasPrefix(secret, tasks.DaemonSetTaskSecretPrefix) {
			continue
		}

		found := false
		for _, node := range nodes {
			if secret == tasks.GetDaemonSetTaskSecretName(node) {
				found = true
			}
		}
		if !found {
			dropSecrets[secret] = struct{}{}
		}
	}

	for secretName := range dropSecrets {
		Slowdown() // 避免高频操作
		logger.Infof("remove secret %s", secretName)
		if err := secretClient.Delete(c.ctx, secretName, metav1.DeleteOptions{}); err != nil {
			if !errors.IsNotFound(err) {
				c.mm.IncHandledSecretFailedCounter(secretName, define.ActionDelete)
				logger.Errorf("failed to delete secret %s, err: %s", secretName, err)
			}
			continue
		}
		c.mm.IncHandledSecretSuccessCounter(secretName, define.ActionDelete)
	}
}

// createOrUpdateStatefulSetTaskSecrets 创建 statefulset sercets
// statefulset 为 external 类型的采集 指定 statefulset worker 进行采集 任务采用 hash 分配
func (c *Operator) createOrUpdateStatefulSetTaskSecrets(childConfigs []*discover.ChildConfig) {
	n := c.statefulSetWorker
	if n <= 0 {
		if ConfEnableStatefulSetWorker {
			logger.Warn("no available statefulset worker found")
		}
		return
	}

	c.reconcileStatefulSetWorker(len(childConfigs))

	// 排序子配置文件
	sort.Slice(childConfigs, func(i, j int) bool {
		return childConfigs[i].FileName < childConfigs[j].FileName
	})

	parseHost := func(s string) string {
		u, err := url.Parse(s)
		if err != nil {
			return ""
		}
		return u.Host
	}

	workers := c.objectsController.GetPods(ConfStatefulSetWorkerRegex)
	logger.Infof("found statefulset workers: %#v", workers)
	antiNodeConfigs := make([]*discover.ChildConfig, 0)

	currTasksCache := make(map[int]map[string]struct{})
	groups := make([][]*discover.ChildConfig, n)
	for idx, config := range childConfigs {
		var mod int
		if ConfStatefulSetDispatchType == dispatchTypeRoundrobin {
			mod = idx % n // 轮训算法
		} else {
			mod = int(config.Hash() % uint64(n)) // 默认为 hash 分配
		}

		// 检查是否命中反亲和规则
		var matchAntiAffinity bool
		if config.AntiAffinity {
			h := parseHost(config.Address)
			if _, ok := workers[h]; ok {
				antiNodeConfigs = append(antiNodeConfigs, config)
				matchAntiAffinity = true
			}
		}
		// 命中了则不再继续分配
		if matchAntiAffinity {
			continue
		}

		groups[mod] = append(groups[mod], config)
		c.recorder.updateConfigNode(config.FileName, fmt.Sprintf("worker%d", mod))

		if _, ok := currTasksCache[mod]; !ok {
			currTasksCache[mod] = make(map[string]struct{})
		}
		currTasksCache[mod][config.FileName] = struct{}{}
	}

	for i := 0; i < len(antiNodeConfigs); i++ {
		config := antiNodeConfigs[i]

		// 取出 IP 与 host 相同的 worker 并避开
		h := parseHost(config.Address)
		w := workers[h]
		mod := (w.Index + 1) % len(workers)
		groups[mod] = append(groups[mod], config)
		logger.Infof("worker match antiaffinity rules, host=%s, worker%d", h, mod)

		c.recorder.updateConfigNode(config.FileName, fmt.Sprintf("worker%d", mod))
		if _, ok := currTasksCache[mod]; !ok {
			currTasksCache[mod] = make(map[string]struct{})
		}
		currTasksCache[mod][config.FileName] = struct{}{}
	}

	maxSecretsAllowed := int(float64(c.objectsController.NodeCount()) * ConfMaxNodeSecretRatio)
	count := 0

	if statefulsetAlarmer.Alarm() {
		c.statefulSetTaskCache = map[int]map[string]struct{}{}
		logger.Info("statefulset worker resynced")
	}

	secretClient := c.client.CoreV1().Secrets(ConfMonitorNamespace)
	for idx, configs := range groups {
		Slowdown()
		secretName := tasks.GetStatefulSetTaskSecretName(idx)
		cache := c.statefulSetTaskCache[idx]
		if len(cache) > 0 && EqualMap(currTasksCache[idx], cache) {
			logger.Infof("secrets %s nothing changed, skipped", secretName)
			continue
		}

		count++
		if count > maxSecretsAllowed {
			c.mm.IncSecretsExceededCounter()
			logger.Errorf("statefulset tasks exceeded, maxAllowed=%d, current=%d", maxSecretsAllowed, count)
			continue
		}

		bytesTotal := 0
		secret := newSecret(tasks.GetStatefulSetTaskSecretName(idx), tasks.TaskTypeStatefulSet)
		for _, config := range configs {
			compressed, err := compressor.Compress(config.Data)
			if err != nil {
				logger.Errorf("failed to compress config content, addr=%s, err: %v", config.Address, err)
				continue
			}

			bytesTotal += len(compressed)
			secret.Data[config.FileName] = compressed
			logger.Debugf("statefulset secret %s add file %s", secret.Name, config.FileName)
		}

		logger.Infof("statefulset secret %s contains %d files", secret.Name, len(secret.Data))
		c.mm.SetActiveSecretFileCount(tasks.TaskTypeStatefulSet, secret.Name, len(secret.Data))

		if err := k8sutils.CreateOrUpdateSecret(c.ctx, secretClient, secret); err != nil {
			c.mm.IncHandledSecretFailedCounter(secret.Name, define.ActionCreateOrUpdate)
			logger.Errorf("failed to create or update secret: %v", err)
			delete(currTasksCache, idx)
			continue
		}
		c.mm.IncHandledSecretSuccessCounter(secret.Name, define.ActionCreateOrUpdate)
		logger.Infof("create or update statefulset secret %s", secret.Name)
	}
	c.statefulSetTaskCache = currTasksCache
}

// cleanupStatefulSetChildSecret 清理 statefulset secrets
func (c *Operator) cleanupStatefulSetChildSecret() {
	n := c.statefulSetWorker
	if n <= 0 {
		if ConfEnableStatefulSetWorker {
			logger.Warn("no available statefulset worker found")
		}
		return
	}

	nextState := make(map[string]bool)
	for i := 0; i < n; i++ {
		nextState[tasks.GetStatefulSetTaskSecretName(i)] = true
	}
	// 最新状态
	prevState := make(map[string]bool)
	c.statefulSetSecretMut.Lock()
	for k := range c.statefulSetSecretMap {
		prevState[k] = true
	}
	c.statefulSetSecretMut.Unlock()

	secretClient := c.client.CoreV1().Secrets(ConfMonitorNamespace)
	// 如果最新状态中存在 但下一轮的状态中不存在的话 则删除 secrets
	for prev := range prevState {
		if !nextState[prev] {
			Slowdown()
			logger.Infof("remove secret %s", prev)
			if err := secretClient.Delete(c.ctx, prev, metav1.DeleteOptions{}); err != nil {
				if !errors.IsNotFound(err) {
					c.mm.IncHandledSecretFailedCounter(prev, define.ActionDelete)
					logger.Errorf("failed to delete secret %s, err: %s", prev, err)
				}
				continue
			}
			c.mm.IncHandledSecretSuccessCounter(prev, define.ActionDelete)
		}
	}
}

func (c *Operator) collectChildConfigs() ([]*discover.ChildConfig, []*discover.ChildConfig) {
	var statefulset []*discover.ChildConfig
	var daemonset []*discover.ChildConfig

	c.discoversMut.Lock()
	var records []ConfigFileRecord
	for _, dis := range c.discovers {
		for _, cfg := range dis.StatefulSetChildConfigs() {
			records = append(records, NewConfigFileRecord(dis, cfg))
		}
		for _, cfg := range dis.DaemonSetChildConfigs() {
			records = append(records, NewConfigFileRecord(dis, cfg))
		}

		statefulset = append(statefulset, dis.StatefulSetChildConfigs()...)
		daemonset = append(daemonset, dis.DaemonSetChildConfigs()...)
	}
	c.discoversMut.Unlock()
	c.recorder.updateConfigFiles(records)
	return statefulset, daemonset
}

func (c *Operator) dispatchTasks() {
	if ConfDryRun {
		logger.Info("dryrun mode, skip dispatch")
		return
	}
	c.mm.IncDispatchedTaskCounter()
	now := time.Now()

	statefulset, daemonset := c.collectChildConfigs()
	c.createOrUpdateChildSecret(statefulset, daemonset)
	c.mm.ObserveDispatchedTaskDuration(now)
}

func newSecret(name string, taskType string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ConfMonitorNamespace,
			Labels: map[string]string{
				"createdBy":         "bkmonitor-operator",
				tasks.LabelTaskType: taskType,
			},
		},

		Data: map[string][]byte{},
	}
}
