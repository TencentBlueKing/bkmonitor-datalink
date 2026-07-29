// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package podterminatingreporter

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type StateInitOptions struct {
	Namespace          string
	StateConfigMapName string
	RequestTimeout     time.Duration
	StateMaxBytes      int
}

func (o StateInitOptions) Validate() error {
	if errors := validation.IsDNS1123Label(o.Namespace); len(errors) > 0 {
		return fmt.Errorf("invalid namespace %q: %s", o.Namespace, strings.Join(errors, ", "))
	}
	if errors := validation.IsDNS1123Subdomain(o.StateConfigMapName); len(errors) > 0 {
		return fmt.Errorf("invalid state ConfigMap name %q: %s", o.StateConfigMapName, strings.Join(errors, ", "))
	}
	if o.RequestTimeout <= 0 {
		return fmt.Errorf("request timeout must be positive")
	}
	if o.StateMaxBytes <= 0 || o.StateMaxBytes > HardMaxStateBytes {
		return fmt.Errorf("state max bytes %d exceeds hard limit %d or is not positive", o.StateMaxBytes, HardMaxStateBytes)
	}
	return nil
}

func RunStateInitInCluster(ctx context.Context, options StateInitOptions) error {
	if err := options.Validate(); err != nil {
		return err
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("create in-cluster Kubernetes config: %w", err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create Kubernetes client: %w", err)
	}
	return EnsureStateConfigMap(
		ctx,
		client,
		options.Namespace,
		options.StateConfigMapName,
		options.RequestTimeout,
		options.StateMaxBytes,
	)
}
