// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package discover

import (
	"context"
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/elastic/beats/libbeat/common/transport/tlscommon"
	"github.com/goware/urlx"
	"github.com/pkg/errors"
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/discovery/targetgroup"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/relabel"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	bkv1beta1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/apis/crd/v1beta1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/kits"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/target"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	Base64Protocol = "base64://"
)

var bus = kits.NewDefaultRateBus()

func Publish() { bus.Publish() }

func Notify() <-chan struct{} { return bus.Subscribe() }

// Discover 是监控资源监视器
type Discover interface {
	// Name 实例名称 discover 唯一标识
	Name() string

	// Type 实例类型 目前有 endpoints、pod，ingress
	Type() string

	// IsSystem 是否为系统内置资源
	IsSystem() bool

	// Start 启动实例
	Start() error

	// Stop 停止实例
	Stop()

	// Reload 重载实例
	Reload() error

	// MonitorMeta 返回元数据信息
	MonitorMeta() define.MonitorMeta

	// DataID 获取 DataID 信息
	DataID() *bkv1beta1.DataID

	// SetDataID 更新 DataID 信息
	SetDataID(dataID *bkv1beta1.DataID)

	// DaemonSetChildConfigs 获取 daemonset 类型子配置信息
	DaemonSetChildConfigs() []*ChildConfig

	// StatefulSetChildConfigs 获取 statafulset 类型子配置信息
	StatefulSetChildConfigs() []*ChildConfig
}

// ChildConfig 子任务配置文件信息
type ChildConfig struct {
	Meta      define.MonitorMeta
	Node      string
	FileName  string
	Address   string
	Data      []byte
	Scheme    string
	Path      string
	Mask      string
	TaskType  string
	Namespace string
}

func (c ChildConfig) String() string {
	return fmt.Sprintf("Node=%s, FileName=%s, Address=%s, Data=%s", c.Node, c.FileName, c.Address, string(c.Data))
}

func (c ChildConfig) Hash() uint64 {
	h := fnv.New64a()
	h.Write([]byte(c.Node))
	h.Write(c.Data)
	h.Write([]byte(c.Mask))
	return h.Sum64()
}

func EncodeBase64(s string) string {
	return Base64Protocol + base64.StdEncoding.EncodeToString([]byte(s))
}

type BaseParams struct {
	Client                 kubernetes.Interface
	RelabelRule            string
	RelabelIndex           string
	Name                   string
	KubeConfig             string
	Namespaces             []string
	Path                   string
	Scheme                 string
	ProxyURL               string
	Period                 string
	Timeout                string
	ForwardLocalhost       bool
	DisableCustomTimestamp bool
	DataID                 *bkv1beta1.DataID
	Relabels               []*relabel.Config
	BasicAuth              *promv1.BasicAuth
	TLSConfig              *promv1.TLSConfig
	BearerTokenFile        string
	BearerTokenSecret      *corev1.SecretKeySelector
	ExtraLabels            map[string]string
	System                 bool
	UrlValues              url.Values
	MetricRelabelConfigs   []yaml.MapSlice
}

type BaseDiscover struct {
	*BaseParams
	parentCtx         context.Context
	ctx               context.Context
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	role              string
	monitorMeta       define.MonitorMeta
	mm                *metricMonitor
	checkIfNodeExists define.CheckFunc
	fetched           bool

	// 任务配置文件信息 通过 source 进行分组 使用 hash 进行唯一校验
	childConfigMut    sync.RWMutex
	childConfigGroups map[string]map[uint64]*ChildConfig // map[targetGroup.Source]map[hash]*ChildConfig
}

func NewBaseDiscover(ctx context.Context, role string, monitorMeta define.MonitorMeta, checkFn define.CheckFunc, params *BaseParams) *BaseDiscover {
	return &BaseDiscover{
		parentCtx:         ctx,
		role:              role,
		BaseParams:        params,
		checkIfNodeExists: checkFn,
		monitorMeta:       monitorMeta,
		mm:                newMetricMonitor(params.Name),
	}
}

func (d *BaseDiscover) getUrlValues() url.Values {
	if d.UrlValues == nil {
		return nil
	}
	values := make(map[string][]string)
	for k, items := range d.UrlValues {
		for _, item := range items {
			values[k] = append(values[k], item)
		}
	}
	return values
}

func (d *BaseDiscover) getNamespaces() []string {
	namespaces := d.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{corev1.NamespaceAll}
	}
	return namespaces
}

func (d *BaseDiscover) Type() string {
	return "base"
}

func (d *BaseDiscover) Name() string {
	return d.BaseParams.Name
}

func (d *BaseDiscover) IsSystem() bool {
	return d.System
}

func (d *BaseDiscover) DataID() *bkv1beta1.DataID {
	return d.BaseParams.DataID
}

func (d *BaseDiscover) MonitorMeta() define.MonitorMeta {
	return d.monitorMeta
}

func (d *BaseDiscover) PreStart() {
	d.mm.IncStartedCounter()
	d.ctx, d.cancel = context.WithCancel(d.parentCtx)
	d.childConfigGroups = make(map[string]map[uint64]*ChildConfig)
	logger.Infof("starting discover %s", d.Name())
}

func (d *BaseDiscover) SetDataID(dataID *bkv1beta1.DataID) {
	d.BaseParams.DataID = dataID
	d.BaseParams.ExtraLabels = dataID.Spec.Labels
}

func (d *BaseDiscover) String() string {
	return fmt.Sprintf("Name=%s, Type=%s, Namespace=%v, System=%v", d.Name(), d.Type(), d.getNamespaces(), d.System)
}

func (d *BaseDiscover) Stop() {
	d.cancel()
	logger.Infof("waiting discover %s", d.Name())

	d.wg.Wait()
	d.mm.IncStoppedCounter()
	logger.Infof("shutting discover %s", d.Name())
}

func (d *BaseDiscover) makeMetricTarget(lbls, origLabels labels.Labels, namespace string) (*target.MetricTarget, error) {
	metricTarget := &target.MetricTarget{}
	var isNodeType bool
	var targetName string
	taskType := tasks.TaskTypeStatefulSet

	// model.* 相关 label 有可能会被重写 使用 lbls（保证一定有 __address__ 字段）
	for _, label := range lbls {
		switch label.Name {
		case model.AddressLabel:
			metricTarget.Address = label.Value
		case model.SchemeLabel:
			metricTarget.Scheme = label.Value
		case model.MetricsPathLabel:
			metricTarget.Path = label.Value
		}
	}

	// 这里是通过原始 label 查找固定字段，所以使用的还是 combinedlabels
	for _, label := range origLabels {
		switch label.Name {
		// 补充 NodeName
		case labelEndpointNodeName, labelPodNodeName:
			metricTarget.NodeName = label.Value

			// 如果 target 类型是 node，则需要特殊处理，此时 endpointNodeName 对应 label 会为空
		case labelEndpointAddressTargetKind, labelPodAddressTargetKind:
			if label.Value == "Node" {
				isNodeType = true
			}
		case labelEndpointAddressTargetName, labelPodAddressTargetName:
			targetName = label.Value
		}
	}

	if isNodeType {
		metricTarget.NodeName = targetName
	}

	if d.checkIfNodeExists != nil {
		nodeName, exist := d.checkIfNodeExists(metricTarget.NodeName)
		if exist {
			taskType = tasks.TaskTypeDaemonSet
		}
		// 修正 nodename
		metricTarget.NodeName = nodeName
	}

	if metricTarget.NodeName == "" {
		logger.Debugf("no node info from labels: %+v", origLabels)
		metricTarget.NodeName = define.UnknownNode
	}

	// 初始化参数列表
	metricTarget.Params = d.getUrlValues()
	if d.UrlValues == nil {
		metricTarget.Params = make(url.Values)
	}

	// 匹配优先级
	// 1) from labels
	// 2) from struct
	if metricTarget.Scheme == "" {
		metricTarget.Scheme = d.Scheme
	}
	if metricTarget.Path == "" {
		metricTarget.Path = d.Path
	}

	requestURL, err := url.Parse(metricTarget.Path)
	if err != nil {
		return nil, errors.Wrap(err, "parse request path failed")
	}
	metricTarget.Path = requestURL.Path

	params, err := url.ParseQuery(requestURL.RawQuery)
	if err != nil {
		return nil, errors.Wrap(err, "parse request query failed")
	}
	for key := range params {
		metricTarget.Params[key] = append(metricTarget.Params[key], params[key]...)
	}

	if d.BasicAuth != nil && d.BasicAuth.Username.String() != "" && d.BasicAuth.Password.String() != "" {
		secretClient := d.Client.CoreV1().Secrets(d.monitorMeta.Namespace)
		username, err := k8sutils.GetSecretDataBySecretKeySelector(d.ctx, secretClient, d.BasicAuth.Username)
		if err != nil {
			return nil, errors.Wrap(err, "get username from secret failed")
		}

		password, err := k8sutils.GetSecretDataBySecretKeySelector(d.ctx, secretClient, d.BasicAuth.Password)
		if err != nil {
			return nil, errors.Wrap(err, "get password from secret failed")
		}

		metricTarget.Username = username
		metricTarget.Password = password
	}

	metricTarget.BearerTokenFile = d.BearerTokenFile
	if d.BearerTokenSecret != nil && d.BearerTokenSecret.Name != "" && d.BearerTokenSecret.Key != "" {
		secretClient := d.Client.CoreV1().Secrets(d.monitorMeta.Namespace)
		bearerToken, err := k8sutils.GetSecretDataBySecretKeySelector(d.ctx, secretClient, *d.BearerTokenSecret)
		if err != nil {
			return nil, errors.Wrapf(err, "get bearer token from secret failed, monitor=%s", d.monitorMeta.ID())
		}
		metricTarget.BearerToken = bearerToken
	}

	if d.TLSConfig != nil {
		metricTarget.TLSConfig = &tlscommon.Config{}
		secretClient := d.Client.CoreV1().Secrets(d.monitorMeta.Namespace)
		if d.TLSConfig.CAFile != "" {
			metricTarget.TLSConfig.CAs = []string{d.TLSConfig.CAFile}
		}
		if d.TLSConfig.CA.Secret != nil {
			ca, err := k8sutils.GetSecretDataBySecretKeySelector(d.ctx, secretClient, *d.TLSConfig.CA.Secret)
			if err != nil {
				return nil, errors.Wrapf(err, "get TLS CA from secret failed, monitor=%s", d.monitorMeta.ID())
			}
			metricTarget.TLSConfig.CAs = []string{EncodeBase64(ca)}
		}

		if d.TLSConfig.CertFile != "" {
			metricTarget.TLSConfig.Certificate.Certificate = d.TLSConfig.CertFile
		}
		if d.TLSConfig.Cert.Secret != nil {
			cert, err := k8sutils.GetSecretDataBySecretKeySelector(d.ctx, secretClient, *d.TLSConfig.Cert.Secret)
			if err != nil {
				return nil, errors.Wrapf(err, "get TLS Cert from secret failed, monitor=%s", d.monitorMeta.ID())
			}
			metricTarget.TLSConfig.Certificate.Certificate = EncodeBase64(cert)
		}

		if d.TLSConfig.KeyFile != "" {
			metricTarget.TLSConfig.Certificate.Key = d.TLSConfig.KeyFile
		}
		if d.TLSConfig.KeySecret != nil {
			key, err := k8sutils.GetSecretDataBySecretKeySelector(d.ctx, secretClient, *d.TLSConfig.KeySecret)
			if err != nil {
				return nil, errors.Wrapf(err, "get TLS Key from secret failed, monitor=%s", d.monitorMeta.ID())
			}
			metricTarget.TLSConfig.Certificate.Key = EncodeBase64(key)
		}
	}

	if len(lbls) == 0 {
		metricTarget.Labels = origLabels
	} else {
		metricTarget.Labels = lbls
	}

	metricTarget.Meta = d.monitorMeta
	metricTarget.ExtraLabels = d.ExtraLabels
	metricTarget.Namespace = namespace // 采集目标的 namespace
	metricTarget.DataID = d.DataID().Spec.DataID
	metricTarget.DimensionReplace = d.DataID().Spec.DimensionReplace
	metricTarget.MetricReplace = d.DataID().Spec.MetricReplace
	metricTarget.MetricRelabelConfigs = d.MetricRelabelConfigs
	metricTarget.Period = d.Period
	metricTarget.Timeout = d.Timeout
	metricTarget.ProxyURL = d.ProxyURL
	metricTarget.Mask = d.Mask()
	metricTarget.TaskType = taskType
	metricTarget.RelabelRule = d.RelabelRule
	metricTarget.RelabelIndex = d.RelabelIndex

	return metricTarget, nil
}

func (d *BaseDiscover) StatefulSetChildConfigs() []*ChildConfig {
	d.childConfigMut.RLock()
	defer d.childConfigMut.RUnlock()

	configs := make([]*ChildConfig, 0)
	for _, group := range d.childConfigGroups {
		for _, cfg := range group {
			if cfg.TaskType == tasks.TaskTypeStatefulSet {
				configs = append(configs, cfg)
			}
		}
	}
	return configs
}

func (d *BaseDiscover) DaemonSetChildConfigs() []*ChildConfig {
	d.childConfigMut.RLock()
	defer d.childConfigMut.RUnlock()

	configs := make([]*ChildConfig, 0)
	for _, group := range d.childConfigGroups {
		for _, cfg := range group {
			if cfg.TaskType == tasks.TaskTypeDaemonSet {
				configs = append(configs, cfg)
			}
		}
	}
	return configs
}

func (d *BaseDiscover) Mask() string {
	var mask string
	conv := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}

	mask += conv(d.System)
	return mask
}

// loopHandleTargetGroup 持续处理来自 k8s 的 targets
func (d *BaseDiscover) loopHandleTargetGroup() {
	defer Publish()

	const duration = 5
	const resync = 100 // 避免事件丢失

	ticker := time.NewTicker(time.Second * duration)
	defer ticker.Stop()

	counter := 0
	for {
		select {
		case <-d.ctx.Done():
			return

		case <-ticker.C:
			counter++
			tgList, updatedAt := GetTargetGroups(d.role, d.getNamespaces())
			logger.Debugf("discover %s updated at: %v", d.Name(), time.Unix(updatedAt, 0))
			if time.Now().Unix()-updatedAt > duration*2 && counter%resync != 0 && d.fetched {
				logger.Debugf("discover %s found nothing changed, skip targetgourps handled", d.Name())
				continue
			}
			d.fetched = true
			logger.Debugf("discover %s starts to handle targets", d.Name())
			for _, tg := range tgList {
				if tg == nil {
					continue
				}
				logger.Debugf("discover %s get targets source: %s, targets: %+v, labels: %+v", d.Name(), tg.Source, tg.Targets, tg.Labels)
				d.handleTargetGroup(tg)
			}
			logger.Debugf("discover %s handle targets done", d.Name())
		}
	}
}

func forwardAddress(addr string) (string, error) {
	withSchema := strings.HasPrefix(addr, "https") || strings.HasPrefix(addr, "http")

	u, err := urlx.Parse(addr)
	if err != nil {
		return "", err
	}

	port := u.Port()
	if port != "" {
		u.Host = "127.0.0.1:" + port
	} else {
		u.Host = "127.0.0.1"
	}
	if !withSchema {
		u.Scheme = ""
		return u.String()[2:], nil
	}

	return u.String(), nil
}

func metaFromSource(s string) (string, string, error) {
	parts := strings.Split(s, "/")
	if len(parts) != 3 {
		return "", "", errors.Errorf("invalid source: %v", s)
	}
	return parts[1], parts[2], nil
}

// handleTargetGroup 遍历自身的所有 target group 计算得到活跃的 target 并删除消失的 target
func (d *BaseDiscover) handleTargetGroup(targetGroup *targetgroup.Group) {
	namespace, _, err := metaFromSource(targetGroup.Source)
	if err != nil {
		logger.Errorf("failed to parse source: %v", err)
		return
	}

	sourceName := targetGroup.Source
	childConfigs := make([]*ChildConfig, 0)

	for _, tlset := range targetGroup.Targets {
		lbls := make(labels.Labels, 0, len(tlset)+len(targetGroup.Labels))
		for ln, lv := range tlset {
			lbls = append(lbls, labels.Label{
				Name:  string(ln),
				Value: string(lv),
			})
		}
		for ln, lv := range targetGroup.Labels {
			if _, ok := tlset[ln]; !ok {
				lbls = append(lbls, labels.Label{
					Name:  string(ln),
					Value: string(lv),
				})
			}
		}

		sort.Sort(lbls)
		res, orig, err := d.populateLabels(lbls)
		if err != nil {
			d.mm.IncCreatedChildConfigFailedCounter()
			logger.Errorf("failed to populate labels: %v", err)
			continue
		}
		if len(res) == 0 {
			continue
		}

		logger.Debugf("discover %s populate labels %+v", d.Name(), res)
		metricTarget, err := d.makeMetricTarget(res, orig, namespace)
		if err != nil {
			d.mm.IncCreatedChildConfigFailedCounter()
			logger.Errorf("failed to make metric target: %v", err)
			continue
		}

		if d.ForwardLocalhost {
			metricTarget.Address, err = forwardAddress(metricTarget.Address)
			if err != nil {
				d.mm.IncCreatedChildConfigFailedCounter()
				logger.Errorf("failed to forward address: %v, err: %v", metricTarget.Address, err)
				continue
			}
		}

		metricTarget.DisableCustomTimestamp = d.DisableCustomTimestamp
		data, err := metricTarget.YamlBytes()
		if err != nil {
			d.mm.IncCreatedChildConfigFailedCounter()
			logger.Errorf("failed to marshal target, err: %s", err)
			continue
		}

		d.mm.IncCreatedChildConfigSuccessCounter()
		childConfig := &ChildConfig{
			Node:      metricTarget.NodeName,
			FileName:  metricTarget.FileName(),
			Address:   metricTarget.Address,
			Data:      data,
			Scheme:    metricTarget.Scheme,
			Path:      metricTarget.Path,
			Mask:      metricTarget.Mask,
			Meta:      metricTarget.Meta,
			Namespace: metricTarget.Namespace,
			TaskType:  metricTarget.TaskType,
		}
		logger.Debugf("discover %s create child config: %v", d.Name(), childConfig)
		childConfigs = append(childConfigs, childConfig)
	}

	d.notify(sourceName, childConfigs)
}

// notify 判断是否刷新文件配置 需要则要发送通知信号
func (d *BaseDiscover) notify(source string, childConfigs []*ChildConfig) {
	d.childConfigMut.Lock()
	defer d.childConfigMut.Unlock()

	if _, ok := d.childConfigGroups[source]; !ok {
		d.childConfigGroups[source] = make(map[uint64]*ChildConfig)
	}

	added := make(map[uint64]struct{})
	var changed bool

	// 增加新出现的配置
	for _, cfg := range childConfigs {
		hash := cfg.Hash()
		if _, ok := d.childConfigGroups[source][hash]; !ok {
			logger.Infof("discover %s adds file, node=%s, filename=%s", d.Name(), cfg.Node, cfg.FileName)
			d.childConfigGroups[source][hash] = cfg
			changed = true
		}
		added[hash] = struct{}{}
	}

	// 删除已经消失的配置
	removed := make([]uint64, 0)
	for key := range d.childConfigGroups[source] {
		if _, ok := added[key]; !ok {
			removed = append(removed, key)
			changed = true
		}
	}

	for _, key := range removed {
		cfg := d.childConfigGroups[source][key]
		logger.Infof("discover %s deletes file, node=%s, filename=%s", d.Name(), cfg.Node, cfg.FileName)
		delete(d.childConfigGroups[source], key)
	}

	// 如果文件有变更则发送通知
	if changed {
		logger.Infof("discover %s found targetgroup.source changed", source)
		Publish()
	}
}

// populateLabels builds a label set from the given label set and scrape configuration.
// It returns a label set before relabeling was applied as the second return value.
// Returns the original discovered label set found before relabelling was applied if the target is dropped during relabeling.
func (d *BaseDiscover) populateLabels(lset labels.Labels) (res, orig labels.Labels, err error) {
	// Copy labels into the labelset for the target if they are not set already.
	scrapeLabels := []labels.Label{
		{Name: model.JobLabel, Value: d.Name()},
		{Name: model.MetricsPathLabel, Value: d.Path},
		{Name: model.SchemeLabel, Value: d.Scheme},
	}
	lb := labels.NewBuilder(lset)

	for _, l := range scrapeLabels {
		if lv := lset.Get(l.Name); lv == "" {
			lb.Set(l.Name, l.Value)
		}
	}
	// Encode scrape query parameters as labels.
	// for k, v := range d.UrlValues {
	// 	if len(v) > 0 {
	// 		lb.Set(model.ParamLabelPrefix+k, v[0])
	// 	}
	// }

	preRelabelLabels := lb.Labels()
	lset = relabel.Process(preRelabelLabels, d.Relabels...)

	// Check if the target was dropped.
	if lset == nil {
		return nil, preRelabelLabels, nil
	}
	if v := lset.Get(model.AddressLabel); v == "" {
		return nil, nil, errors.New("no address")
	}

	lb = labels.NewBuilder(lset)

	// addPort checks whether we should add a default port to the address.
	// If the address is not valid, we don't append a port either.
	addPort := func(s string) bool {
		// If we can split, a port exists and we don't have to add one.
		if _, _, err := net.SplitHostPort(s); err == nil {
			return false
		}
		// If adding a port makes it valid, the previous error
		// was not due to an invalid address and we can append a port.
		_, _, err := net.SplitHostPort(s + ":1234")
		return err == nil
	}
	addr := lset.Get(model.AddressLabel)
	// If it's an address with no trailing port, infer it based on the used scheme.
	if addPort(addr) {
		// Addresses reaching this point are already wrapped in [] if necessary.
		switch lset.Get(model.SchemeLabel) {
		case "http", "":
			addr = addr + ":80"
		case "https":
			addr = addr + ":443"
		default:
			return nil, nil, errors.Errorf("invalid scheme: %q", d.Scheme)
		}
		lb.Set(model.AddressLabel, addr)
	}

	if err := config.CheckTargetAddress(model.LabelValue(addr)); err != nil {
		return nil, nil, err
	}

	// Meta labels are deleted after relabelling. Other internal labels propagate to
	// the target which decides whether they will be part of their label set.
	for _, l := range lset {
		if strings.HasPrefix(l.Name, model.MetaLabelPrefix) {
			lb.Del(l.Name)
		}
	}

	// Default the instance label to the target address.
	if v := lset.Get(model.InstanceLabel); v == "" {
		lb.Set(model.InstanceLabel, addr)
	}

	res = lb.Labels()
	for _, l := range res {
		// Check label values are valid, drop the target if not.
		if !model.LabelValue(l.Value).IsValid() {
			return nil, nil, errors.Errorf("invalid label value for %q: %q", l.Name, l.Value)
		}
	}
	return res, preRelabelLabels, nil
}
