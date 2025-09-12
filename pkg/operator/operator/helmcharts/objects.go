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
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type ReleaseElement struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Revision   int    `json:"revision"`
	Updated    string `json:"updated"`
	Status     string `json:"status"`
	Chart      string `json:"chart"`
	AppVersion string `json:"app_version"`
}

var b64 = base64.StdEncoding

var magicGzip = []byte{0x1f, 0x8b, 0x08}

type innerKey struct {
	Namespace string
	Name      string
}

type Objects struct {
	mut      sync.RWMutex
	elements map[innerKey]ReleaseElement
}

func (o *Objects) Set(element ReleaseElement) {
	o.mut.Lock()
	defer o.mut.Unlock()

	ik := innerKey{
		Namespace: element.Namespace,
		Name:      element.Name,
	}

	v, ok := o.elements[ik]
	if !ok || v.Revision <= element.Revision {
		o.elements[ik] = element
	}
}

func (o *Objects) Del(element ReleaseElement) {
	o.mut.Lock()
	defer o.mut.Unlock()

	ik := innerKey{
		Namespace: element.Namespace,
		Name:      element.Name,
	}
	delete(o.elements, ik)
}

func (o *Objects) Range(visitFunc func(ele ReleaseElement)) {
	o.mut.RLock()
	defer o.mut.RUnlock()

	for _, ele := range o.elements {
		visitFunc(ele)
	}
}

func newHelmChartsObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*Objects, error) {
	genericInformer, err := sharedInformer.ForResource(corev1.SchemeGroupVersion.WithResource("secrets"))
	if err != nil {
		return nil, err
	}

	informer := genericInformer.Informer()
	err = informer.SetTransform(func(obj any) (any, error) {
		secret, ok := obj.(*corev1.Secret)
		if !ok {
			logger.Errorf("excepted Secret type, got %T", obj)
			return obj, nil // 原路返回
		}
		newObj := &corev1.Secret{}
		newObj.Name = secret.Name
		newObj.Namespace = secret.Namespace
		newObj.Data = secret.Data
		return newObj, nil
	})
	if err != nil {
		return nil, err
	}

	objs := &Objects{elements: make(map[innerKey]ReleaseElement)}
	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			secret, ok := obj.(*corev1.Secret)
			if !ok {
				logger.Errorf("excepted Secret type, got %T", obj)
				return
			}

			rls, err := decodeRelease(string(secret.Data["release"]))
			if err != nil {
				logger.Errorf("failed to decode release: %s", err)
				return
			}
			objs.Set(castReleaseElement(rls))
		},
		UpdateFunc: func(oldObj, newObj any) {
			old, ok := oldObj.(*corev1.Secret)
			if !ok {
				logger.Errorf("expected Secret type, got %T", oldObj)
				return
			}
			cur, ok := newObj.(*corev1.Secret)
			if !ok {
				logger.Errorf("expected Secret type, got %T", newObj)
				return
			}
			if old.ResourceVersion == cur.ResourceVersion {
				logger.Debugf("Secret '%s/%s' does not change", old.Namespace, old.Name)
				return
			}

			rls, err := decodeRelease(string(cur.Data["release"]))
			if err != nil {
				logger.Errorf("failed to decode release: %s", err)
				return
			}
			objs.Set(castReleaseElement(rls))
		},
		DeleteFunc: func(obj any) {
			secret, ok := obj.(*corev1.Secret)
			if !ok {
				logger.Errorf("excepted Secret type, got %T", obj)
				return
			}

			rls, err := decodeRelease(string(secret.Data["release"]))
			if err != nil {
				logger.Errorf("failed to decode release: %s", err)
				return
			}
			objs.Del(castReleaseElement(rls))
		},
	})
	if err != nil {
		return nil, err
	}

	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, "HelmCharts", informer)
	if !synced {
		return nil, errors.New("failed to sync HelmCharts caches")
	}
	return objs, nil
}

type Info struct {
	LastDeployed time.Time `json:"last_deployed,omitempty"`
	Status       string    `json:"status,omitempty"`
}

type Metadata struct {
	Name       string `json:"name,omitempty"`
	Version    string `json:"version,omitempty"`
	AppVersion string `json:"appVersion,omitempty"`
}

type Chart struct {
	Metadata *Metadata `json:"metadata"`
}

type Release struct {
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Version   int    `json:"version,omitempty"`
	Info      *Info  `json:"info,omitempty"`
	Chart     *Chart `json:"chart,omitempty"`
}

// decodeRelease decodes the bytes of data into a release
// type. Data must contain a base64 encoded gzipped string of a
// valid release, otherwise an error is returned.
func decodeRelease(data string) (*Release, error) {
	// base64 decode string
	b, err := b64.DecodeString(data)
	if err != nil {
		return nil, err
	}

	// For backwards compatibility with releases that were stored before
	// compression was introduced we skip decompression if the
	// gzip magic header is not found
	if len(b) > 3 && bytes.Equal(b[0:3], magicGzip) {
		r, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		b2, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		b = b2
	}

	var rls Release
	// unmarshal release object bytes
	if err := json.Unmarshal(b, &rls); err != nil {
		return nil, err
	}
	return &rls, nil
}

func castReleaseElement(r *Release) ReleaseElement {
	element := ReleaseElement{
		Name:       r.Name,
		Namespace:  r.Namespace,
		Revision:   r.Version,
		Status:     r.Info.Status,
		Chart:      formatChartName(r.Chart),
		AppVersion: formatAppVersion(r.Chart),
	}
	updated := "-"
	if tspb := r.Info.LastDeployed; !tspb.IsZero() {
		updated = tspb.Format(time.RFC3339)
	}
	element.Updated = updated
	return element
}

func formatChartName(c *Chart) string {
	if c == nil || c.Metadata == nil {
		// This is an edge case that has happened in prod, though we don't
		// know how: https://github.com/helm/helm/issues/1347
		return "MISSING"
	}
	return fmt.Sprintf("%s-%s", c.Metadata.Name, c.Metadata.Version)
}

func formatAppVersion(c *Chart) string {
	if c == nil || c.Metadata == nil {
		// This is an edge case that has happened in prod, though we don't
		// know how: https://github.com/helm/helm/issues/1347
		return "MISSING"
	}
	return c.Metadata.AppVersion
}
