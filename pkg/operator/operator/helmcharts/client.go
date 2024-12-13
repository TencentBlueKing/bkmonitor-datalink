// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package helmcharts

import (
	"context"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
)

var helmchartsRevision = promauto.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: define.MonitorNamespace,
		Name:      "helm_charts_revision",
		Help:      "helm charts revision",
	},
	[]string{"name", "namespace", "revision", "updated", "status", "chart", "app_version"},
)

func newMetricMonitor() *metricMonitor {
	return &metricMonitor{}
}

type metricMonitor struct{}

func (m *metricMonitor) SetHelmChartsRevision(element ReleaseElement) {
	helmchartsRevision.WithLabelValues(
		element.Name,
		element.Namespace,
		strconv.Itoa(element.Revision),
		element.Updated,
		element.Status,
		element.Chart,
		element.AppVersion,
	).Set(float64(element.Revision))
}

type Controller struct {
	ctx     context.Context
	cancel  context.CancelFunc
	objects *Objects
	mm      *metricMonitor
}

func NewController(ctx context.Context, client kubernetes.Interface) (*Controller, error) {
	labelOptions := informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
		opts.LabelSelector = "owner=helm"
	})

	namespace := informers.WithNamespace(configs.G().MonitorNamespace) // TODO(mando): 目前仅监听 operator 组件所处 namespace
	sharedInformer := informers.NewSharedInformerFactoryWithOptions(client, define.ReSyncPeriod, namespace, labelOptions)

	ctx, cancel := context.WithCancel(ctx)
	objs, err := newHelmChartsObjects(ctx, sharedInformer)
	if err != nil {
		cancel()
		return nil, err
	}

	return &Controller{
		ctx:     ctx,
		cancel:  cancel,
		objects: objs,
		mm:      newMetricMonitor(),
	}, nil
}

func (c *Controller) UpdateMetrics() {
	c.objects.Range(func(ele ReleaseElement) {
		c.mm.SetHelmChartsRevision(ele)
	})
}

func (c *Controller) Stop() {
	c.cancel()
}

func (c *Controller) GetByNamespace(namespace string) []ReleaseElement {
	var eles []ReleaseElement
	c.objects.Range(func(ele ReleaseElement) {
		if ele.Namespace == namespace {
			eles = append(eles, ele)
		}
	})
	return eles
}
