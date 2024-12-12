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
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/promfmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
)

type Controller struct {
	ctx     context.Context
	cancel  context.CancelFunc
	objects *Objects
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
	}, nil
}

func (c *Controller) Stop() {
	c.cancel()
}

func (c *Controller) WriteInfoMetrics(w io.Writer) {
	c.objects.Range(func(ele ReleaseElement) {
		promfmt.FmtBytes(w, promfmt.Metric{
			Name: "helm_charts_info",
			Labels: []promfmt.Label{
				{Name: "name", Value: ele.Name},
				{Name: "namespace", Value: ele.Namespace},
				{Name: "revision", Value: ele.Revision},
				{Name: "updated", Value: ele.Updated},
				{Name: "status", Value: ele.Status},
				{Name: "chart", Value: ele.Chart},
				{Name: "app_version", Value: ele.AppVersion},
			},
		})
	})
}
