// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kubernetesd

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/elastic/beats/libbeat/common/transport/tlscommon"
	"github.com/pkg/errors"
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	promk8ssd "github.com/prometheus/prometheus/discovery/kubernetes"
	"github.com/prometheus/prometheus/model/labels"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/eplabels"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/logx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover/shareddiscovery"
)

const (
	TypePod = "pod"
)

func TypeEndpoints(endpointslice bool) string {
	if endpointslice {
		return "endpointslice"
	}
	return "endpoints"
}

const (
	labelPodNodeName          = "__meta_kubernetes_pod_node_name"
	labelPodAddressTargetKind = "__meta_kubernetes_pod_address_target_kind"
	labelPodAddressTargetName = "__meta_kubernetes_pod_address_target_name"
)

type BasicAuthRaw struct {
	Username string
	Password string
}

type Options struct {
	*discover.CommonOptions

	KubeConfig        string
	Namespaces        []string
	Client            kubernetes.Interface
	BasicAuth         *promv1.BasicAuth
	BasicAuthRaw      BasicAuthRaw
	TLSConfig         *promv1.TLSConfig
	BearerTokenSecret *corev1.SecretKeySelector
	UseEndpointSlice  bool
}

type Discover struct {
	*discover.BaseDiscover

	ctx  context.Context
	role string
	opts *Options
}

var _ discover.Discover = (*Discover)(nil)

func New(ctx context.Context, role string, opts *Options) *Discover {
	d := &Discover{
		ctx:          ctx,
		role:         role,
		opts:         opts,
		BaseDiscover: discover.NewBaseDiscover(ctx, opts.CommonOptions),
	}

	d.SetUK(fmt.Sprintf("%s:%s", role, strings.Join(d.getNamespaces(), "/")))
	d.SetHelper(discover.Helper{
		AccessBasicAuth:   d.accessBasicAuth,
		AccessBearerToken: d.accessBearerToken,
		AccessTlsConfig:   d.accessTLSConfig,
		MatchNodeName:     d.matchNodeName,
	})
	return d
}

func (d *Discover) Type() string {
	return d.role
}

func (d *Discover) Reload() error {
	d.Stop()
	return d.Start()
}

type WrapDiscovery struct {
	*promk8ssd.Discovery
}

func (WrapDiscovery) Stop() {}

func (d *Discover) Start() error {
	d.PreStart()

	err := shareddiscovery.Register(d.UK(), func() (*shareddiscovery.SharedDiscovery, error) {
		cfg := promk8ssd.DefaultSDConfig
		cfg.Role = promk8ssd.Role(d.role)
		cfg.NamespaceDiscovery.Names = d.getNamespaces()
		cfg.KubeConfig = d.opts.KubeConfig

		discovery, err := promk8ssd.New(logx.New(d.Type()), &cfg)
		if err != nil {
			return nil, errors.Wrap(err, d.Type())
		}
		return shareddiscovery.New(d.UK(), &WrapDiscovery{discovery}), nil
	})
	if err != nil {
		return err
	}

	go d.LoopHandle()
	return nil
}

func (d *Discover) getNamespaces() []string {
	namespaces := d.opts.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{corev1.NamespaceAll}
	}
	return namespaces
}

func (d *Discover) matchNodeName(lbs labels.Labels) string {
	var target string
	var isNodeType bool

	// 这里是通过原始 label 查找固定字段，所以使用的还是 combinedlabels
	for _, label := range lbs {
		switch label.Name {
		// 补充 NodeName
		case eplabels.EndpointNodeName(d.opts.UseEndpointSlice), labelPodNodeName:
			return label.Value

			// 如果 target 类型是 node，则需要特殊处理，此时 endpointNodeName 对应 label 会为空
		case eplabels.EndpointAddressTargetKind(d.opts.UseEndpointSlice), labelPodAddressTargetKind:
			if label.Value == "Node" {
				isNodeType = true
			}

		case eplabels.EndpointAddressTargetName(d.opts.UseEndpointSlice), labelPodAddressTargetName:
			target = label.Value
		}
	}

	if isNodeType {
		return target // 仅当为 nodetype 时返回 target
	}
	return ""
}

func (d *Discover) accessBasicAuth() (string, string, error) {
	raw := d.opts.BasicAuthRaw
	// 优先使用 raw 配置 当且仅当两者不为空时才生效
	if raw.Username != "" && raw.Password != "" {
		return raw.Username, raw.Password, nil
	}

	return d.accessBasicAuthFromSecret()
}

func (d *Discover) accessBasicAuthFromSecret() (string, string, error) {
	auth := d.opts.BasicAuth
	if auth == nil || auth.Username.String() == "" || auth.Password.String() == "" {
		return "", "", nil
	}

	secretClient := d.opts.Client.CoreV1().Secrets(d.opts.MonitorMeta.Namespace)
	username, err := k8sutils.GetSecretDataBySecretKeySelector(d.ctx, secretClient, auth.Username)
	if err != nil {
		return "", "", errors.Wrap(err, "get username from secret failed")
	}
	password, err := k8sutils.GetSecretDataBySecretKeySelector(d.ctx, secretClient, auth.Password)
	if err != nil {
		return "", "", errors.Wrap(err, "get password from secret failed")
	}

	return username, password, nil
}

func (d *Discover) accessBearerToken() (string, error) {
	secret := d.opts.BearerTokenSecret
	if secret == nil || secret.Name == "" || secret.Key == "" {
		return "", nil
	}

	secretClient := d.opts.Client.CoreV1().Secrets(d.opts.MonitorMeta.Namespace)
	bearerToken, err := k8sutils.GetSecretDataBySecretKeySelector(d.ctx, secretClient, *secret)
	if err != nil {
		return "", errors.Wrap(err, "get bearer token from secret failed")
	}
	return bearerToken, nil
}

func (d *Discover) accessTLSConfig() (*tlscommon.Config, error) {
	if d.opts.TLSConfig == nil {
		return nil, nil
	}

	tlsConfig := &tlscommon.Config{}
	secretClient := d.opts.Client.CoreV1().Secrets(d.opts.MonitorMeta.Namespace)
	if d.opts.TLSConfig.CAFile != "" {
		tlsConfig.CAs = []string{d.opts.TLSConfig.CAFile}
	}
	if d.opts.TLSConfig.CA.Secret != nil {
		ca, err := k8sutils.GetSecretDataBySecretKeySelector(d.ctx, secretClient, *d.opts.TLSConfig.CA.Secret)
		if err != nil {
			return nil, errors.Wrap(err, "get TLS CA from secret failed")
		}
		tlsConfig.CAs = []string{encodeBase64(ca)}
	}

	if d.opts.TLSConfig.CertFile != "" {
		tlsConfig.Certificate.Certificate = d.opts.TLSConfig.CertFile
	}
	if d.opts.TLSConfig.Cert.Secret != nil {
		cert, err := k8sutils.GetSecretDataBySecretKeySelector(d.ctx, secretClient, *d.opts.TLSConfig.Cert.Secret)
		if err != nil {
			return nil, errors.Wrap(err, "get TLS Cert from secret failed")
		}
		tlsConfig.Certificate.Certificate = encodeBase64(cert)
	}

	if d.opts.TLSConfig.KeyFile != "" {
		tlsConfig.Certificate.Key = d.opts.TLSConfig.KeyFile
	}
	if d.opts.TLSConfig.KeySecret != nil {
		key, err := k8sutils.GetSecretDataBySecretKeySelector(d.ctx, secretClient, *d.opts.TLSConfig.KeySecret)
		if err != nil {
			return nil, errors.Wrap(err, "get TLS Key from secret failed")
		}
		tlsConfig.Certificate.Key = encodeBase64(key)
	}

	return tlsConfig, nil
}

func encodeBase64(s string) string {
	return "base64://" + base64.StdEncoding.EncodeToString([]byte(s))
}
