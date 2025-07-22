// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package k8sutils

import (
	"time"

	prominfs "github.com/prometheus-operator/prometheus-operator/pkg/informers"
	"github.com/prometheus-operator/prometheus-operator/pkg/listwatch"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"

	bkcli "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/client/clientset/versioned"
	bkextinfs "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/client/informers/externalversions"
)

func NewBKInformerFactories(
	allowNamespaces, denyNamespaces map[string]struct{},
	bkCli bkcli.Interface,
	defaultResync time.Duration,
	tweakListOptions func(*metav1.ListOptions),
) prominfs.FactoriesForNamespaces {
	tweaks, namespaces := newInformerOptions(
		allowNamespaces, denyNamespaces, tweakListOptions,
	)

	opts := []bkextinfs.SharedInformerOption{bkextinfs.WithTweakListOptions(tweaks)}

	ret := monitoringInformersForNamespaces{}
	for _, namespace := range namespaces {
		opts = append(opts, bkextinfs.WithNamespace(namespace))
		ret[namespace] = bkextinfs.NewSharedInformerFactoryWithOptions(bkCli, defaultResync, opts...)
	}

	return ret
}

type monitoringInformersForNamespaces map[string]bkextinfs.SharedInformerFactory

func (i monitoringInformersForNamespaces) Namespaces() sets.String {
	return sets.StringKeySet(i)
}

func (i monitoringInformersForNamespaces) ForResource(namespace string, resource schema.GroupVersionResource) (prominfs.InformLister, error) {
	return i[namespace].ForResource(resource)
}

// newInformerOptions returns a list option tweak function and a list of namespaces
// based on the given allowed and denied namespaces.
//
// If allowedNamespaces contains one only entry equal to k8s.io/apimachinery/pkg/apis/meta/v1.NamespaceAll
// then it returns it and a tweak function filtering denied namespaces using a field selector.
//
// Else, denied namespaces are ignored and just the set of allowed namespaces is returned.
//
// Copied from github.com/prometheus-operator/prometheus-operator
func newInformerOptions(allowedNamespaces, deniedNamespaces map[string]struct{}, tweaks func(*metav1.ListOptions)) (func(*metav1.ListOptions), []string) {
	if tweaks == nil {
		tweaks = func(*metav1.ListOptions) {} // nop
	}

	var namespaces []string

	if listwatch.IsAllNamespaces(allowedNamespaces) {
		return func(options *metav1.ListOptions) {
			tweaks(options)
			listwatch.DenyTweak(options, "metadata.namespace", deniedNamespaces)
		}, []string{metav1.NamespaceAll}
	}

	for ns := range allowedNamespaces {
		namespaces = append(namespaces, ns)
	}

	return tweaks, namespaces
}
