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
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type monitorResourceLister interface {
	ListAll(selector labels.Selector, appendFn cache.AppendFunc) error
}

type monitorDiscoverBuilder func(obj any) []discover.Discover

func (c *Operator) recoverMonitorDiscovers(
	lister monitorResourceLister,
	builder monitorDiscoverBuilder,
) error {
	c.monitorReconcileMut.Lock()
	defer c.monitorReconcileMut.Unlock()

	return lister.ListAll(labels.Everything(), func(obj any) {
		for _, dis := range builder(obj) {
			added, err := c.addDiscoverIfAbsent(dis)
			if err != nil {
				logger.Errorf("recover discover %v failed: %s", dis, err)
				continue
			}
			if added {
				logger.Infof("recover discover %v", dis)
			}
		}
	})
}

func (c *Operator) serviceMonitorDiscoversFromObject(obj any) []discover.Discover {
	serviceMonitor, ok := obj.(*promv1.ServiceMonitor)
	if !ok {
		logger.Errorf("expected ServiceMonitor type, got %T", obj)
		return nil
	}
	if ifRejectServiceMonitor(serviceMonitor) {
		return nil
	}
	return c.createServiceMonitorDiscovers(serviceMonitor)
}

func (c *Operator) recoverServiceMonitorDiscovers() error {
	if c.serviceMonitorInformer == nil {
		return nil
	}
	return c.recoverMonitorDiscovers(c.serviceMonitorInformer, c.serviceMonitorDiscoversFromObject)
}

func (c *Operator) podMonitorDiscoversFromObject(obj any) []discover.Discover {
	podMonitor, ok := obj.(*promv1.PodMonitor)
	if !ok {
		logger.Errorf("expected PodMonitor type, got %T", obj)
		return nil
	}
	if ifRejectPodMonitor(podMonitor) {
		return nil
	}
	return c.createPodMonitorDiscovers(podMonitor)
}

func (c *Operator) recoverPodMonitorDiscovers() error {
	if c.podMonitorInformer == nil {
		return nil
	}
	return c.recoverMonitorDiscovers(c.podMonitorInformer, c.podMonitorDiscoversFromObject)
}

func (c *Operator) recoverPromScrapeConfigDiscovers() {
	c.promSdReconcileMut.Lock()
	defer c.promSdReconcileMut.Unlock()

	for _, dis := range c.createPromScrapeConfigDiscovers(c.snapshotPromScrapeConfigs()) {
		added, err := c.addDiscoverIfAbsent(dis)
		if err != nil {
			logger.Errorf("recover prom scrapeConfig discover %v failed: %s", dis, err)
			continue
		}
		if added {
			logger.Infof("recover prom scrapeConfig discover %v", dis)
		}
	}
}

func (c *Operator) recoverDataIDDependentDiscovers() {
	if err := c.recoverServiceMonitorDiscovers(); err != nil {
		logger.Errorf("recover serviceMonitor discovers failed: %s", err)
	}
	if err := c.recoverPodMonitorDiscovers(); err != nil {
		logger.Errorf("recover podMonitor discovers failed: %s", err)
	}
	c.recoverPromScrapeConfigDiscovers()
}
