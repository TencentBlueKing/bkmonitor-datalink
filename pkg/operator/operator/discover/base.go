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
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/elastic/beats/libbeat/common/transport/tlscommon"
	"github.com/goware/urlx"
	"github.com/pkg/errors"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/relabel"
	"gopkg.in/yaml.v2"

	bkv1beta1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/apis/monitoring/v1beta1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/feature"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/labelspool"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover/shareddiscovery"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/target"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Helper 一系列执行函数 由 discover 自行实现
type Helper struct {
	AccessBasicAuth   func() (string, string, error)
	AccessBearerToken func() (string, error)
	AccessTlsConfig   func() (*tlscommon.Config, error)
	MatchNodeName     func(labels.Labels) string
}

// CommonOptions baseDiscover 通用的 Options
type CommonOptions struct {
	MonitorMeta            define.MonitorMeta
	UniqueKey              string
	RelabelRule            string
	RelabelIndex           string
	NormalizeMetricName    bool
	AntiAffinity           bool
	Name                   string
	Path                   string
	Scheme                 string
	ProxyURL               string
	Period                 string
	Timeout                string
	ForwardLocalhost       bool
	DisableCustomTimestamp bool
	DataID                 *bkv1beta1.DataID
	Relabels               []*relabel.Config
	BearerTokenFile        string
	ExtraLabels            map[string]string
	System                 bool
	UrlValues              url.Values
	MetricRelabelConfigs   []yaml.MapSlice
	MatchSelector          map[string]string
	DropSelector           map[string]string
	LabelJoinMatcher       *feature.LabelJoinMatcherSpec
	CheckNodeNameFunc      func(string) (string, bool)
	NodeLabelsFunc         func(string) map[string]string
}

type BaseDiscover struct {
	opts        *CommonOptions
	parentCtx   context.Context
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	monitorMeta define.MonitorMeta
	mm          *shareddiscovery.MetricMonitor
	fetched     bool
	cache       *hashCache
	helper      Helper

	// 任务配置文件信息 通过 source 进行分组 使用 hash 进行唯一校验
	childConfigMut    sync.RWMutex
	childConfigGroups map[string]map[uint64]*ChildConfig // map[targetGroup.Source]map[hash]*ChildConfig
}

func NewBaseDiscover(ctx context.Context, opts *CommonOptions) *BaseDiscover {
	return &BaseDiscover{
		parentCtx:   ctx,
		opts:        opts,
		monitorMeta: opts.MonitorMeta,
		mm:          shareddiscovery.NewMetricMonitor(opts.Name),
	}
}

func (d *BaseDiscover) getUrlValues() url.Values {
	if d.opts.UrlValues == nil {
		return nil
	}
	values := make(map[string][]string)
	for k, items := range d.opts.UrlValues {
		for _, item := range items {
			values[k] = append(values[k], item)
		}
	}
	return values
}

func (d *BaseDiscover) SetUK(s string) {
	d.opts.UniqueKey = s
}

func (d *BaseDiscover) SetHelper(helper Helper) {
	d.helper = helper
}

func (d *BaseDiscover) UK() string {
	return d.opts.UniqueKey
}

func (d *BaseDiscover) Type() string {
	return "base"
}

func (d *BaseDiscover) Name() string {
	return d.opts.Name
}

func (d *BaseDiscover) IsSystem() bool {
	return d.opts.System
}

func (d *BaseDiscover) DataID() *bkv1beta1.DataID {
	return d.opts.DataID
}

func (d *BaseDiscover) MonitorMeta() define.MonitorMeta {
	return d.monitorMeta
}

func (d *BaseDiscover) PreStart() {
	d.mm.IncStartedCounter()

	d.ctx, d.cancel = context.WithCancel(d.parentCtx)
	d.childConfigGroups = make(map[string]map[uint64]*ChildConfig)
	d.cache = newHashCache(d.opts.Name, time.Minute*10)
	logger.Infof("starting discover %s", d.Name())
}

func (d *BaseDiscover) SetDataID(dataID *bkv1beta1.DataID) {
	d.opts.DataID = dataID
	d.opts.ExtraLabels = dataID.Spec.Labels
}

func (d *BaseDiscover) String() string {
	return fmt.Sprintf("Name=%s, Type=%s, System=%v", d.Name(), d.Type(), d.opts.System)
}

func (d *BaseDiscover) Stop() {
	d.cancel()
	logger.Infof("waiting discover %s", d.Name())

	d.wg.Wait()
	d.mm.IncStoppedCounter()
	d.cache.Clean()
	logger.Infof("shutting discover %s", d.Name())
}

func (d *BaseDiscover) makeMetricTarget(lbls, origLabels labels.Labels, namespace string) (*target.MetricTarget, error) {
	metricTarget := &target.MetricTarget{}
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

	if d.helper.MatchNodeName != nil {
		metricTarget.NodeName = d.helper.MatchNodeName(origLabels)
	}

	if d.opts.CheckNodeNameFunc != nil {
		nodeName, exist := d.opts.CheckNodeNameFunc(metricTarget.NodeName)
		if exist {
			taskType = tasks.TaskTypeDaemonSet
		}
		// 修正 nodename
		metricTarget.NodeName = nodeName
	}

	if metricTarget.NodeName == "" {
		logger.Debugf("%s no node info from labels: %+v", d.Name(), origLabels)
		metricTarget.NodeName = define.UnknownNode
	}

	// 初始化参数列表
	metricTarget.Params = d.getUrlValues()
	if d.opts.UrlValues == nil {
		metricTarget.Params = make(url.Values)
	}

	if metricTarget.Scheme == "" {
		metricTarget.Scheme = d.opts.Scheme
	}
	if metricTarget.Path == "" {
		metricTarget.Path = d.opts.Path
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

	if d.helper.AccessBasicAuth != nil {
		username, password, err := d.helper.AccessBasicAuth()
		if err != nil {
			return nil, err
		}
		metricTarget.Username = username
		metricTarget.Password = password
	}

	if d.helper.AccessBearerToken != nil {
		bearerToken, err := d.helper.AccessBearerToken()
		if err != nil {
			return nil, err
		}
		metricTarget.BearerToken = bearerToken
	}

	if d.helper.AccessTlsConfig != nil {
		tlsConfig, err := d.helper.AccessTlsConfig()
		if err != nil {
			return nil, err
		}
		metricTarget.TLSConfig = tlsConfig
	}

	if len(lbls) == 0 {
		metricTarget.Labels = origLabels
	} else {
		metricTarget.Labels = lbls
	}

	period := d.opts.Period
	if period == "" {
		period = configs.G().DefaultPeriod
	}
	timeout := d.opts.Timeout
	if timeout == "" {
		timeout = period
	}

	metricTarget.Meta = d.monitorMeta
	metricTarget.ExtraLabels = d.opts.ExtraLabels
	metricTarget.Namespace = namespace // 采集目标的 namespace
	metricTarget.DataID = d.DataID().Spec.DataID
	metricTarget.DimensionReplace = d.DataID().Spec.DimensionReplace
	metricTarget.MetricReplace = d.DataID().Spec.MetricReplace
	metricTarget.MetricRelabelConfigs = d.opts.MetricRelabelConfigs
	metricTarget.Period = period
	metricTarget.Timeout = timeout
	metricTarget.BearerTokenFile = d.opts.BearerTokenFile
	metricTarget.ProxyURL = d.opts.ProxyURL
	metricTarget.Mask = d.Mask()
	metricTarget.TaskType = taskType
	metricTarget.RelabelRule = d.opts.RelabelRule
	metricTarget.RelabelIndex = d.opts.RelabelIndex
	metricTarget.NormalizeMetricName = d.opts.NormalizeMetricName
	metricTarget.LabelJoinMatcher = d.opts.LabelJoinMatcher
	metricTarget.NodeLabelsFunc = d.opts.NodeLabelsFunc

	return metricTarget, nil
}

func (d *BaseDiscover) StatefulSetChildConfigs() []*ChildConfig {
	d.childConfigMut.RLock()
	defer d.childConfigMut.RUnlock()

	cfgs := make([]*ChildConfig, 0)
	for _, group := range d.childConfigGroups {
		for _, cfg := range group {
			if cfg.TaskType == tasks.TaskTypeStatefulSet {
				cfgs = append(cfgs, cfg)
			}
		}
	}
	return cfgs
}

func (d *BaseDiscover) DaemonSetChildConfigs() []*ChildConfig {
	d.childConfigMut.RLock()
	defer d.childConfigMut.RUnlock()

	cfgs := make([]*ChildConfig, 0)
	for _, group := range d.childConfigGroups {
		for _, cfg := range group {
			if cfg.TaskType == tasks.TaskTypeDaemonSet {
				cfgs = append(cfgs, cfg)
			}
		}
	}
	return cfgs
}

func (d *BaseDiscover) Mask() string {
	var mask string
	conv := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}

	mask += conv(d.opts.System)
	return mask
}

func (d *BaseDiscover) LoopHandle() {
	d.wg.Add(1)
	defer d.wg.Done()

	d.loopHandleTargetGroup()
}

// loopHandleTargetGroup 持续处理来自 k8s 的 targets
func (d *BaseDiscover) loopHandleTargetGroup() {
	defer Publish()

	const resync = 100 // 避免事件丢失

	// 保证在调度周期内至少能够同步一次即可
	duration := configs.G().DispatchInterval / 2
	if duration < 5 {
		duration = 5
	}

	// 打散执行时刻 尽量减少内存抖动
	delay := time.Now().Nanosecond() % int(duration)
	if delay > 0 {
		time.Sleep(time.Second * time.Duration(delay))
	}
	logger.Infof("%s fetch interval (%ds), delay (%ds) and ready to sync targets", d.Name(), duration, delay)

	ticker := time.NewTicker(time.Second * time.Duration(duration))
	defer ticker.Stop()

	counter := 0
	for {
		select {
		case <-d.ctx.Done():
			return

		case <-ticker.C:
			counter++
			// 避免 skip 情况下多申请不必要的内存
			updatedAt := shareddiscovery.FetchTargetGroupsUpdatedAt(d.UK())
			logger.Debugf("%s updated at: %v", d.Name(), time.Unix(updatedAt, 0))
			if time.Now().Unix()-updatedAt > duration*2 && counter%resync != 0 && d.fetched {
				logger.Debugf("%s found nothing changed, skip targetgourps handled", d.Name())
				continue
			}
			d.fetched = true

			// 真正需要变更时才 fetch targetgroups
			tgList := shareddiscovery.FetchTargetGroups(d.UK())
			for _, tg := range tgList {
				logger.Debugf("%s get targets source: %s, targets: %+v, labels: %+v", d.Name(), tg.Source, tg.Targets, tg.Labels)
				d.handleTargetGroup(tg)
			}
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

func tgSourceNamespace(s string) string {
	parts := strings.Split(s, "/")
	if len(parts) == 3 && parts[1] != "" {
		return parts[1]
	}
	return "-"
}

func matchSelector(labels []labels.Label, selector map[string]string) bool {
	var count int
	for k, v := range selector {
		re, err := regexp.Compile(v)
		if err != nil {
			logger.Errorf("failed to compile expr '%s', err: %v", v, err)
			continue
		}
		for _, lbs := range labels {
			if lbs.Name == k {
				if !re.MatchString(lbs.Value) {
					return false
				}
				count++
				break
			}
		}
	}
	return count == len(selector)
}

func (d *BaseDiscover) handleTarget(namespace string, tlset, tglbs labels.Labels) (*ChildConfig, error) {
	lbls := labelspool.Get()
	defer labelspool.Put(lbls)

	for _, lb := range tlset {
		lbls = append(lbls, labels.Label{
			Name:  lb.Name,
			Value: lb.Value,
		})
	}

	isIn := func(name string) bool {
		for i := 0; i < len(tlset); i++ {
			if tlset[i].Name == name {
				return true
			}
		}
		return false
	}

	for _, lb := range tglbs {
		if isIn(lb.Name) {
			continue
		}

		lbls = append(lbls, labels.Label{
			Name:  lb.Name,
			Value: lb.Value,
		})
	}

	// annotations 白名单过滤
	if len(d.opts.MatchSelector) > 0 {
		if !matchSelector(lbls, d.opts.MatchSelector) {
			logger.Debugf("%s annotation selector not match: %v", d.Name(), d.opts.MatchSelector)
			return nil, nil
		}
	}

	// annotations 黑名单过滤
	if len(d.opts.DropSelector) > 0 {
		if matchSelector(lbls, d.opts.DropSelector) {
			logger.Debugf("%s annotation selector drop: %v", d.Name(), d.opts.DropSelector)
			return nil, nil
		}
	}

	sort.Sort(lbls)
	res, orig, err := d.populateLabels(lbls)
	if err != nil {
		return nil, errors.Wrap(err, "populate labels failed")
	}
	if len(res) == 0 {
		return nil, nil
	}

	logger.Debugf("%s populate labels %+v", d.Name(), res)
	metricTarget, err := d.makeMetricTarget(res, orig, namespace)
	if err != nil {
		return nil, errors.Wrap(err, "make metric target failed")
	}

	interval, _ := time.ParseDuration(metricTarget.Period)
	d.mm.SetMonitorScrapeInterval(interval.Seconds())

	if d.opts.ForwardLocalhost {
		metricTarget.Address, err = forwardAddress(metricTarget.Address)
		if err != nil {
			return nil, errors.Wrapf(err, "forward address failed, address=%s", metricTarget.Address)
		}
	}

	metricTarget.DisableCustomTimestamp = d.opts.DisableCustomTimestamp
	data, err := metricTarget.YamlBytes()
	if err != nil {
		return nil, errors.Wrap(err, "marshal target failed")
	}

	childConfig := &ChildConfig{
		Node:         metricTarget.NodeName,
		FileName:     metricTarget.FileName(),
		Address:      metricTarget.Address,
		Data:         data,
		Scheme:       metricTarget.Scheme,
		Path:         metricTarget.Path,
		Mask:         metricTarget.Mask,
		Meta:         metricTarget.Meta,
		Namespace:    metricTarget.Namespace,
		TaskType:     metricTarget.TaskType,
		AntiAffinity: d.opts.AntiAffinity,
	}
	logger.Debugf("%s create child config: %+v", d.Name(), childConfig)
	return childConfig, nil
}

// handleTargetGroup 遍历自身的所有 target group 计算得到活跃的 target 并删除消失的 target
func (d *BaseDiscover) handleTargetGroup(targetGroup *shareddiscovery.WrapTargetGroup) {
	d.mm.IncHandledTgCounter()

	namespace := tgSourceNamespace(targetGroup.Source)
	sourceName := targetGroup.Source
	childConfigs := make([]*ChildConfig, 0)

	for _, tlset := range targetGroup.Targets {
		skipped := d.cache.Check(namespace, tlset, targetGroup.Labels)
		if skipped {
			d.mm.IncCreatedChildConfigCachedCounter()
			continue
		}

		childConfig, err := d.handleTarget(namespace, tlset, targetGroup.Labels)
		if err != nil {
			logger.Errorf("%s handle target failed: %v", d.Name(), err)
			d.mm.IncCreatedChildConfigFailedCounter()
			continue
		}
		if childConfig == nil {
			d.cache.Set(namespace, tlset, targetGroup.Labels)
			continue
		}

		d.mm.IncCreatedChildConfigSuccessCounter()
		childConfigs = append(childConfigs, childConfig)
	}

	d.notify(sourceName, childConfigs)
}

// notify 判断是否刷新文件配置 需要则要发送通知信号
func (d *BaseDiscover) notify(source string, childConfigs []*ChildConfig) {
	d.childConfigMut.Lock()
	defer d.childConfigMut.Unlock()

	// 如果新的 source/childconfigs 为空且之前的缓存也为空 那就无需对比处理了
	if len(childConfigs) == 0 && len(d.childConfigGroups[source]) == 0 {
		logger.Debugf("%s skip handle notify", d.Name())
		return
	}

	if _, ok := d.childConfigGroups[source]; !ok {
		d.childConfigGroups[source] = make(map[uint64]*ChildConfig)
	}

	added := make(map[uint64]struct{})
	var changed bool

	// 增加新出现的配置
	for _, cfg := range childConfigs {
		hash := cfg.Hash()
		if _, ok := d.childConfigGroups[source][hash]; !ok {
			logger.Infof("%s adds file, node=%s, filename=%s", d.Name(), cfg.Node, cfg.FileName)
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
		logger.Infof("%s deletes file, node=%s, filename=%s", d.Name(), cfg.Node, cfg.FileName)
		delete(d.childConfigGroups[source], key)
	}

	// 如果文件有变更则发送通知
	if changed {
		logger.Infof("%s found targetgroup.source changed", source)
		Publish()
	}

	// 删除事件 即后续 source 可能不会再有任何事件了
	if len(d.childConfigGroups[source]) == 0 {
		delete(d.childConfigGroups, source)
		logger.Infof("delete source (%s), cause no childconfigs", source)
	}
}

// populateLabels builds a label set from the given label set and scrape configuration.
// It returns a label set before relabeling was applied as the second return value.
// Returns the original discovered label set found before relabelling was applied if the target is dropped during relabeling.
func (d *BaseDiscover) populateLabels(lset labels.Labels) (res, orig labels.Labels, err error) {
	// Copy labels into the labelset for the target if they are not set already.
	scrapeLabels := []labels.Label{
		{Name: model.JobLabel, Value: d.Name()},
		{Name: model.MetricsPathLabel, Value: d.opts.Path},
		{Name: model.SchemeLabel, Value: d.opts.Scheme},
	}
	lb := labels.NewBuilder(lset)

	for _, l := range scrapeLabels {
		if lv := lset.Get(l.Name); lv == "" {
			lb.Set(l.Name, l.Value)
		}
	}

	preRelabelLabels := lb.Labels(nil)
	lset = relabel.Process(preRelabelLabels, d.opts.Relabels...)

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
			return nil, nil, errors.Errorf("invalid scheme: %q", d.opts.Scheme)
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

	res = lb.Labels(nil)
	for _, l := range res {
		// Check label values are valid, drop the target if not.
		if !model.LabelValue(l.Value).IsValid() {
			return nil, nil, errors.Errorf("invalid label value for %q: %q", l.Name, l.Value)
		}
	}
	return res, preRelabelLabels, nil
}
