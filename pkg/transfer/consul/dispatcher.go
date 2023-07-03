// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"context"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cstockton/go-conv"
	consul "github.com/hashicorp/consul/api"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

type (
	TriggerCreator      func(context.Context, chan *DispatchItem) define.Task
	ShadowCopierCreator func(context.Context) (ShadowCopier, error)
)

// NewPairDispatchInfo :
func NewPairDispatchInfo(target string, pair *KVPair) *define.PairDispatchInfo {
	return &define.PairDispatchInfo{
		Source:  pair.Key,
		Target:  target,
		Version: pair.ModifyIndex,
	}
}

// NewPairDispatchInfoFromShadowed :
func NewPairDispatchInfoFromShadowed(shadowed *KVPair) (*define.PairDispatchInfo, error) {
	pair, err := GetSourceByShadowedPair(shadowed)
	if err != nil {
		return nil, err
	}

	return &define.PairDispatchInfo{
		Source:  pair.Key,
		Target:  shadowed.Key,
		Version: pair.ModifyIndex,
	}, nil
}

// DispatcherConfig :
type DispatcherConfig struct {
	Context         context.Context
	Converter       DispatchConverter
	Client          ClientAPI
	TargetRoot      string
	ManualRoot      string
	TriggerCreator  TriggerCreator
	DispatchDelay   time.Duration
	RecoverInterval time.Duration
}

type DispatchItem struct {
	Sender   string
	Pairs    KVPairs
	Services []*define.ServiceInfo
}

type DispatchItemConf struct {
	Pair   *KVPair
	Config config.PipelineConfig
}

// Dispatcher :
type Dispatcher struct {
	*LeaderMixin
	*DispatcherConfig
	plans        define.PlanWithFlows
	hashBalancer define.Balancer
	autoBalancer define.Balancer
}

// VisitPlan :
func (d *Dispatcher) VisitPlan(fn func(service *define.ServiceDispatchInfo, pair *define.PairDispatchInfo) bool) {
	for _, plan := range d.plans.Plans {
		for _, info := range plan.Pairs {
			if !fn(plan.ServiceDispatchInfo, info) {
				return
			}
		}
	}
}

// Recover : detect exists plan
func (d *Dispatcher) Recover() error {
	api := d.Client.KV()
	logging.Debugf("recover shadow form key: [%s]", d.TargetRoot)
	pairs, _, err := api.List(d.TargetRoot, NewQueryOptions(d.ctx))
	if err != nil {
		return err
	}

	logging.Infof("found %d shadowed links for recover", len(pairs))

	idermap := d.plans.IDers
	flows := d.plans.Flows
	d.plans = define.NewPlanWithFlows()
	d.plans.IDers = idermap
	d.plans.Flows = flows

	for _, pair := range pairs {
		shadowedSource, shadowedTarget, service, err := d.Converter.ShadowDetector(pair)
		if err != nil {
			logging.Errorf("detect shadow link on %s err %v", pair.Key, err)
			continue
		}

		logging.Debugf("detected shadow %s to %s for %s", shadowedSource, shadowedTarget, service)

		info, err := NewPairDispatchInfoFromShadowed(pair)
		if err != nil {
			logging.Errorf("parse shadowed pair on %s err %v", pair.Key, err)
			continue
		}

		plan, ok := d.plans.Plans[service]
		if !ok {
			plan = define.NewServiceDispatchPlan(service)
			d.plans.Plans[service] = plan
		}

		plan.Pairs[info.Source] = info
	}

	return nil
}

func (d *Dispatcher) getElements(pairs KVPairs) ([]define.IDer, []define.IDer) {
	elements := make([]define.IDer, 0, len(pairs))
	for _, pair := range pairs {
		els, err := d.Converter.ElementCreator(pair)
		if err != nil {
			logging.Warnf("create config element from %s error %v, skip", pair.Key, err)
			continue
		}
		elements = append(elements, els...)
	}

	original := make([]define.IDer, 0, len(pairs))
	for _, pair := range pairs {
		org, err := d.repeatOriginalElements(pair)
		if err != nil {
			logging.Warnf("original: create config element from %s error %v, skip", pair.Key, err)
			continue
		}
		original = append(original, org...)
	}

	return elements, original
}

func (d *Dispatcher) repeatOriginalElements(element *consul.KVPair) ([]define.IDer, error) {
	if element == nil {
		return nil, errors.Wrapf(define.ErrOperationForbidden, "pair is nil")
	}

	e := DispatchItemConf{Pair: element}
	err := json.Unmarshal(element.Value, &e.Config)
	if err != nil {
		return nil, err
	}

	var partition int
	if e.Config.MQConfig != nil && e.Config.MQConfig.StorageConfig != nil {
		value, ok := e.Config.MQConfig.StorageConfig["partition"]
		if ok {
			partition = conv.Int(value)
		}
	}
	if partition <= 0 {
		partition = 1
	}

	return utils.NewOriginalDetailsBalanceElementsWithID(e.Config.DataID, &e, partition), nil
}

func (d *Dispatcher) getNodes(infos []*define.ServiceInfo) []define.IDer {
	elements := make([]define.IDer, 0, len(infos))

	for _, service := range infos {
		node, err := d.Converter.NodeCreator(service)
		if err != nil {
			logging.Warnf("create service node from %s error %v, skip", service.ID, err)
			continue

		}
		elements = append(elements, node)
	}

	return elements
}

func (d *Dispatcher) getPairMappings(pairs KVPairs) map[string]*KVPair {
	pairMappings := make(map[string]*KVPair)
	for _, pair := range pairs {
		pairMappings[pair.Key] = pair
	}
	return pairMappings
}

// 从consul获取手动列表
func (d *Dispatcher) getManualList() (map[string][]string, error) {
	result := make(map[string][]string)
	manualRoot := d.ManualRoot
	if !strings.HasSuffix(d.ManualRoot, "/") {
		manualRoot = d.ManualRoot + "/"
	}

	// 从manual路径下获取所有数据
	pairs, _, err := d.Client.KV().List(manualRoot, nil)
	if err != nil {
		logging.Warnf("get manual list err：%s", err)
	}
	// 遍历数据，将里面的value从字符串转换为[]map
	for _, pair := range pairs {
		dataID := path.Base(pair.Key)
		var value []map[string]string
		err := json.Unmarshal(pair.Value, &value)
		if err != nil {
			logging.Errorf("JSON unmarshal %s err:%s", pair.Value, err)
			continue
		}
		for _, item := range value {
			if service, ok := item[ConfServiceNameKey]; ok {
				if list, ok := result[service]; !ok {
					list = make([]string, 1)
					list[0] = dataID
					result[service] = list

				} else {
					list = append(list, dataID)
					result[service] = list
				}
			}
		}

	}

	return result, nil
}

// 将targetService中的按照字典序排序的第一个dataid取出，指向到sourceService，作为均衡交换
// sourceService: 提供data_id的service，表示自动分配得到data_id的transfer service
// targetService: 获得data_id的service，表示手动分配指定得到data_id的transfer service
func (d *Dispatcher) redirectTargetKVPair(sourceService, targetService string, plans map[string]*define.ServiceDispatchPlan) error {
	resultMapping := make(map[string][]*define.PairDispatchInfo)

	targetServicePlan, ok := plans[targetService]
	if !ok {
		return define.ErrMissingTransfer
	}
	sourceServicePlan, ok := plans[sourceService]
	if !ok {
		return define.ErrMissingTransfer
	}

	// 长度大于0表示有自动分配的dataid可以用来均衡
	if len(targetServicePlan.Pairs) <= 0 {
		return nil
	}

	// 需要对keys键值先进行排序，确保在同一个交换的目标我们拿到的key是尽可能的稳定
	pairsKeys := make([]string, 0, len(targetServicePlan.Pairs))
	for key := range targetServicePlan.Pairs {
		pairsKeys = append(pairsKeys, key)
	}
	sort.Strings(pairsKeys)

	// 此处需要循环一直拿到sourceService没有的一个data_id
	for _, key := range pairsKeys {
		pair := targetServicePlan.Pairs[key]

		// 判断目标服务是否已经有过这个data_id的处理了，如果是，则不能再获取这个data_id进行处理
		if _, ok := sourceServicePlan.Pairs[pair.Source]; ok {
			logging.Infof("data_id path->[%s] is already in sourceService->[%s], will try next one", pair.Source, sourceService)
			continue
		}

		// 从pair中拿到dataid
		dataID := path.Base(pair.Source)
		// 将target重定向
		sourceServicePath := path.Join(d.TargetRoot, sourceService, dataID)
		// 将target重定向为source
		pair.Target = sourceServicePath
		// 如果没有列表则增加一个列表
		if _, ok := resultMapping[sourceService]; !ok {
			resultMapping[sourceService] = make([]*define.PairDispatchInfo, 0, 1)
		}
		// 将pair添加到列表中
		resultMapping[sourceService] = append(resultMapping[sourceService], pair)
		// 删除原来的pair
		delete(targetServicePlan.Pairs, key)
		logging.Infof("data_id path->[%s] will switch from->[%s] to->[%s]", pair.Source, targetService, sourceService)
		break
	}
	// 交换的dataid要立刻生效
	return d.addIntoPlans(plans, resultMapping)
}

// 将source dataid重定向到 target dataid
// targetService是指需要接受这个data_id的transfer service
func (d *Dispatcher) redirectSourceKVPair(targetService, dataID string, plans map[string]*define.ServiceDispatchPlan, resultMapping map[string][]*define.PairDispatchInfo) string {
	sourceServiceList := make([]string, 0, len(plans))
	for sourceService := range plans {
		sourceServiceList = append(sourceServiceList, sourceService)
	}
	sort.Strings(sourceServiceList)

	// 寻找要手动分配的dataid
	for _, sourceService := range sourceServiceList {
		sourcePlan := plans[sourceService]

		pairs := sourcePlan.Pairs
		for key, pair := range pairs {
			// 取出dataid
			base := path.Base(pair.Source)
			// 如果匹配，则修改target之后存入resultMapping
			if base == dataID {
				targetPath := path.Join(d.TargetRoot, targetService, dataID)
				// 修改目标信息
				pair.Target = targetPath
				// 如果没有列表则增加
				if _, ok := resultMapping[targetService]; !ok {
					resultMapping[targetService] = make([]*define.PairDispatchInfo, 0, 1)
				}
				// 将pair添加到列表中
				resultMapping[targetService] = append(resultMapping[targetService], pair)
				// 删除原来的pair
				delete(pairs, key)
				// 返回处理的是哪个transfer实例
				logging.Infof("data_id->[%s] is found in source->[%s] will put in service->[%s]", dataID, sourceService, targetService)
				return sourceService
			}
		}
	}
	// 匹配失败返回为空字符
	return ""
}

func (d *Dispatcher) checkServiceExist(targetService string, services []*define.ServiceInfo) bool {
	for _, service := range services {
		if service.ID == targetService {
			return true
		}
	}
	return false
}

func (d *Dispatcher) MakeManualPlan(plans map[string]*define.ServiceDispatchPlan, services []*define.ServiceInfo) error {
	// 记录手动分配计划
	resultMapping := make(map[string][]*define.PairDispatchInfo)

	// 从consul获取manual的数据
	manualList, err := d.getManualList()
	if err != nil {
		logging.Errorf("get manual list failed,error:%s", err)
		// 手动信息获取失败，则直接使用自动分配的信息
		return err
	}
	// 遍历手动分配列表
	for targetService, dataIDList := range manualList {
		// transfer实例不存在,则跳过其相关的再分配流程
		if !d.checkServiceExist(targetService, services) {
			logging.Warnf("target manual service not exist,skip this one,service name:%s", targetService)
			continue
		}

		for _, dataID := range dataIDList {
			// 将plans里的目标dataid的一个实例重定向到target service
			sourceService := d.redirectSourceKVPair(targetService, dataID, plans, resultMapping)
			// 若重定向成功，则source service不为空
			if sourceService != "" {
				// 此时将target service 的一个dataid重定向到 source service，实现交换策略
				err = d.redirectTargetKVPair(sourceService, targetService, plans)
				if err != nil {
					logging.Errorf("switch dataid failed,source:%s,target:%s,dataid:%s,error:%s", sourceService, targetService, dataID, err)
				}
			}
		}
	}
	// 上面手动分配的效果都还没有生效，只是写到了 result mapping里，这里才生效，写入plans
	err = d.addIntoPlans(plans, resultMapping)
	if err != nil {
		logging.Errorf("get error when try to redirect dataid,error:%s", err)
		// 这里原本的plan已经被破坏了，所以只能重新拉取一份
		return err
	}
	return nil
}

// GetPlan 获取自动分配的plan，然后增加手动处理
func (d *Dispatcher) GetPlan(pairs KVPairs, services []*define.ServiceInfo) (define.PlanWithFlows, bool) {
	// 这里取出的是自动均衡分配的 plan
	plans, algo := d.Plan(pairs, services)
	switch algo {
	case define.BalanceAlgoUnknown:
		return define.PlanWithFlows{}, false

	case define.BalanceAlgoHash:
		err := d.MakeManualPlan(plans.Plans, services)
		if err != nil {
			logging.Errorf("get error when make manual plan,error:%s", err)
			return define.PlanWithFlows{}, false
		}
		return plans, true

	default: // define.BalanceAlgoAuto
		return plans, true
	}
}

func (d *Dispatcher) addIntoPlans(plans map[string]*define.ServiceDispatchPlan, resultMapping map[string][]*define.PairDispatchInfo) error {
	// 遍历各个分配结果的内容
	for service, pairList := range resultMapping {

		// 判断已有的分配方案中，是否有这个目标的transfer service
		plan, ok := plans[service]
		if !ok {
			// 如果不存在，那么表示这个配置的生成也是有问题的
			return define.ErrMissingTransfer
		}

		// 将所有的这个分配的结果都写入到最终结果中
		for _, pair := range pairList {
			plan.Pairs[pair.Source] = pair
		}
	}
	return nil
}

func (d *Dispatcher) Balance(plan define.PlanWithFlows, original, items, nodes []define.IDer) (define.IDerMapDetailed, map[string]float64, define.BalanceAlgoType) {
	idersmap := define.NewIDerMapDetailed()
	var flows define.FlowItems
	var code define.AutoError

	// 不启动 autobalancer 的话则默认只使用 hash 分配算法
	if !SchedulerHelper.GetConf().AutoBalanceEnabled {
		idersmap, flows, _ = d.hashBalancer.Balance(plan, items, nodes)
		return idersmap, flows.SumPercentBy(define.FlowItemKeyService), define.BalanceAlgoHash
	}

	idersmap, flows, code = d.autoBalancer.Balance(plan, original, nodes)
	if code != define.AutoErrorNil {
		// consul 挂了 不执行分配操作 等下一轮
		return idersmap, flows.SumPercentBy(define.FlowItemKeyService), define.BalanceAlgoUnknown
	}

	return idersmap, flows.SumPercentBy(define.FlowItemKeyService), define.BalanceAlgoAuto
}

// Plan 通过metadata和transfer实例，生成一个dataid分配草图
func (d *Dispatcher) Plan(pairs KVPairs, services []*define.ServiceInfo) (define.PlanWithFlows, define.BalanceAlgoType) {
	plans := make(map[string]*define.ServiceDispatchPlan)
	// 将pair的详细信息放进了mapping里面，外面只使用service name和dataid
	pairMappings := d.getPairMappings(pairs)

	// elements => metadata上存储的pipeline_config
	elements, original := d.getElements(pairs)
	// nodes => transfer实例名
	nodes := d.getNodes(services)

	// 使用分配算法均分 elements 到 nodes 中
	mappings, flows, algo := d.Balance(d.plans, original, elements, nodes)
	for node, elements := range mappings.All {
		// 遍历element，配合node生成shadow信息
		for _, element := range elements {
			source, target, service, err := d.Converter.ShadowCreator(node, element)
			if err != nil {
				logging.Errorf("shadow link by element %d to node %d create error %v", element.ID(), node.ID(), err)
				continue
			}

			pair, ok := pairMappings[source]
			if !ok {
				logging.Fatalf("pair by source %s not found", source)
			}

			plan, ok := plans[service]
			if !ok {
				plan = define.NewServiceDispatchPlan(service)
				plans[service] = plan
			}

			info := NewPairDispatchInfo(target, pair)
			plan.Pairs[info.Source] = info
		}
	}
	return define.PlanWithFlows{Plans: plans, IDers: mappings, Flows: flows}, algo
}

func (d *Dispatcher) shadowSync(info *define.PairDispatchInfo, source *KVPair) error {
	api := d.Client.KV()

	shadow, err := GetShadowBySourcePair(info.Target, source)
	if err != nil {
		return errors.WithStack(err)
	}

	_, err = api.Put(shadow, NewWriteOptions(d.ctx))
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (d *Dispatcher) addShadowsByPlan(plan *define.ServiceDispatchPlan, pairs map[string]*KVPair) {
	wg := sync.WaitGroup{}
	worker := make(chan struct{}, define.Concurrency())
	for _, info := range plan.Pairs {
		pair, ok := pairs[info.Source]
		if !ok {
			logging.Fatalf("pair by source %s not found", info.Source)
		}

		wg.Add(1)
		worker <- struct{}{}
		go func(info *define.PairDispatchInfo, source *KVPair) {
			defer wg.Done()
			defer func() {
				<-worker
			}()
			logging.Debugf("shadow activate from %s to %s, version %d", info.Source, info.Target, info.Version)
			if err := d.shadowSync(info, pair); err != nil {
				logging.Errorf("shadow sync item %s error %+v", info.Target, err)
			}
		}(info, pair)
	}
	wg.Wait()
}

func (d *Dispatcher) deleteShadowsByPlan(plan *define.ServiceDispatchPlan) {
	api := d.Client.KV()
	targets := make([]string, 0, len(plan.Pairs))
	for _, info := range plan.Pairs {
		targets = append(targets, info.Target)
	}

	wg := sync.WaitGroup{}
	worker := make(chan struct{}, define.Concurrency())
	for _, target := range targets {
		wg.Add(1)
		worker <- struct{}{}
		go func(t string) {
			defer wg.Done()
			defer func() {
				<-worker
			}()
			// 调度信息：${namespace}/service/${version}/${cluster}/data_id/${instance}/${dataid}
			if _, err := api.DeleteTree(t, NewWriteOptions(d.ctx)); err != nil {
				logging.Errorf("shadow delete data_id item %s error %+v", t, err)
			}
		}(target)
	}
	wg.Wait()
}

func ReplaceFlowPrefix(s string) string {
	sp := strings.Split(s, "/")
	const dataidIdx = 4
	if len(sp) >= 5 && sp[dataidIdx] == "data_id" {
		sp[dataidIdx] = "flow"
		return strings.Join(sp, "/")
	}

	return ""
}

func (d *Dispatcher) updateShadowsByPlan(newPlan, oldPlan *define.ServiceDispatchPlan, pairs map[string]*KVPair) {
	oldPairs := oldPlan.Pairs
	for key, info := range newPlan.Pairs {
		oldInfo, ok := oldPairs[key]

		if ok {
			delete(oldPairs, key)
			// not change
			if oldInfo.Version == info.Version {
				continue
			}
		}

		pair, ok := pairs[info.Source]
		if !ok {
			logging.Fatalf("pair by source %s not found", info.Source)
		}

		logging.Debugf("shadow sync from %s to %s, version %d", info.Source, info.Target, info.Version)
		err := d.shadowSync(info, pair)
		if err != nil {
			logging.Errorf("shadow sync item %s error %+v", info.Target, err)
		}
	}
}

// Dispatch :
func (d *Dispatcher) Dispatch(pairs KVPairs, services []*define.ServiceInfo) error {
	if pairs == nil || services == nil {
		return errors.Wrapf(define.ErrValue, "pairs or services is empty")
	}

	newPlans, ok := d.GetPlan(pairs, services)
	if !ok {
		return nil
	}

	oldPlans := d.plans
	d.plans = newPlans
	pairMappings := d.getPairMappings(pairs)

	for service, plan := range newPlans.Plans {
		logging.Debugf("dispatch service %s plan %v", service, plan)
		oldPlan, ok := oldPlans.Plans[service]
		if !ok {
			d.addShadowsByPlan(plan, pairMappings)
			continue
		}

		d.updateShadowsByPlan(plan, oldPlan, pairMappings)
		d.deleteShadowsByPlan(oldPlan)
		delete(oldPlans.Plans, service)
	}

	for service, plan := range oldPlans.Plans {
		logging.Debugf("clean up %d dispatched items for service %s", len(plan.Pairs), service)
		d.deleteShadowsByPlan(plan)
	}

	return nil
}

func (d *Dispatcher) runLoop(ctx context.Context, triggerCh chan *DispatchItem) {
	conf := d.DispatcherConfig
	isUpdate := false

	delayTk := time.NewTicker(conf.DispatchDelay)
	defer delayTk.Stop()

	recoverTk := time.NewTicker(conf.RecoverInterval)
	defer recoverTk.Stop()

	var pairs KVPairs
	var services []*define.ServiceInfo
	updateAt := time.Now()

	checkInterval := SchedulerHelper.ForceGetConf().CheckIntervalDuration()
	flowTicker := time.Tick(checkInterval)
	logging.Infof("autobalancer: flowticker interval: %+v", checkInterval)

	defer logging.Info("dispatcher: quit and no dispatch anymore")

loop:
	for {
		select {
		case <-ctx.Done():
			break loop

		case item := <-triggerCh:
			logging.Debugf("dispatch trigger by: [%#v] ,delay", item)
			isUpdate = true
			if item == nil {
				continue
			}
			pairs = item.Pairs
			services = item.Services
			updateAt = time.Now()

		case <-delayTk.C:
			// 当transfer启动时，pairs，services会先初始化为nil，此时如果trigger没有拿到值，就会误更新
			if updateAt.Add(conf.DispatchDelay).Before(time.Now()) && isUpdate {
				logging.Infof("dispatcher: triggered with %d pairs and %d services", len(pairs), len(services))
				MonitorDispatchTotal.Inc()
				t0 := time.Now()
				if err := d.Dispatch(pairs, services); err != nil {
					logging.Errorf("dispatch error %v", err)
				}
				MonitorDispatchDuration.Observe(time.Since(t0).Seconds())
				isUpdate = false
			}

		case <-flowTicker:
			logging.Infof("dispatcher: flow ticker triggered with %d pairs and %d services", len(pairs), len(services))
			MonitorDispatchTotal.Inc()
			t0 := time.Now()
			if err := d.Dispatch(pairs, services); err != nil {
				logging.Errorf("dispatch error %v", err)
			}
			MonitorDispatchDuration.Observe(time.Since(t0).Seconds())

		case <-recoverTk.C:
			logging.Info("dispatcher: triggered recover")
			err := d.Recover()
			if err != nil {
				logging.Errorf("recover dispatch plan error %v", err)
			}
		}
	}
}

func (d *Dispatcher) run(ctx context.Context) error {
	conf := d.DispatcherConfig
	defer utils.RecoverError(func(e error) {
		logging.Fatalf("dispatcher panic %+v", e)
	})

	taskManager := define.NewTaskManager()

	triggerCh := make(chan *DispatchItem)
	trigger := conf.TriggerCreator(ctx, triggerCh)
	taskManager.Add(trigger)

	err := taskManager.Start()
	if err != nil {
		return err
	}

	err = d.Recover()
	if err != nil {
		return err
	}

	d.runLoop(ctx, triggerCh)

	err = taskManager.Stop()
	if err != nil {
		return err
	}

	close(triggerCh)

	return taskManager.Wait()
}

// NewDispatcher :
func NewDispatcher(conf DispatcherConfig) *Dispatcher {
	balanceConf := SchedulerHelper.ForceGetConf()

	fluctuation := balanceConf.Fluctuation
	forceRound := balanceConf.ForceRound
	balanceLogPath := balanceConf.LogPath

	logging.Infof("autobalancer: balance.conf %+v", balanceConf)

	d := &Dispatcher{
		DispatcherConfig: &conf,
		plans:            define.NewPlanWithFlows(),
		hashBalancer:     utils.NewHashBalancer(),
		autoBalancer:     utils.NewAutoBalancer(fluctuation, forceRound, SchedulerHelper.Flow, SchedulerHelper.GetConf, balanceLogPath),
	}

	d.LeaderMixin = NewLeaderMixin(conf.Context, d.run)
	return d
}

// BaseDispatchTrigger :
type BaseDispatchTrigger struct {
	name string
	ch   chan *DispatchItem
}

// String :
func (t *BaseDispatchTrigger) String() string {
	return t.name
}

// Send :
func (t *BaseDispatchTrigger) Send(item *DispatchItem) {
	if item.Sender == "" {
		item.Sender = t.name
	}
	t.ch <- item
}

// NewBaseDispatchTrigger :
func NewBaseDispatchTrigger(name string, ch chan *DispatchItem) *BaseDispatchTrigger {
	return &BaseDispatchTrigger{
		name: name,
		ch:   ch,
	}
}

// ServiceTrigger :
type ServiceTrigger struct {
	*BaseDispatchTrigger
	define.ServiceWatcher
	prefix string
}

func (t *ServiceTrigger) activate(ctx context.Context, client ClientAPI, services []*define.ServiceInfo) {
	logging.Debugf("service trigger activated")

	api := client.KV()
	var pairs KVPairs

	err := utils.ContextExponentialRetry(ctx, func() error {
		logging.Infof("fetching %s pairs", t.prefix)
		result, _, err := api.List(t.prefix, NewQueryOptions(ctx))
		pairs = result
		return err
	})
	if err != nil {
		logging.Errorf("abort trigger activation because of error %v", err)
		return
	}

	logging.Infof("service trigger dispatch for %d pairs", len(pairs))

	t.Send(&DispatchItem{
		Pairs:    pairs,
		Services: services,
	})
}

// Start :
func (t *ServiceTrigger) Start() error {
	err := t.ServiceWatcher.Start()
	if err != nil {
		return err
	}

	watcher := t.ServiceWatcher

	ctx, err := GetContextFromWatcher(watcher)
	if err != nil {
		return err
	}

	client, err := GetClientFromWatcher(watcher)
	if err != nil {
		return err
	}

	go func() {
	loop:
		for {
			select {
			case <-ctx.Done():
				break loop

			case ev := <-t.Events():
				if ev == nil {
					continue
				}

				nodes, ok := ev.Data.([]*define.ServiceInfo)
				if !ok {
					continue
				}

				t.activate(ctx, client, nodes)
			}
		}
	}()

	return nil
}

// NewServiceTriggerWithWatcher :
func NewServiceTriggerWithWatcher(watcher define.ServiceWatcher, prefix string, ch chan *DispatchItem) *ServiceTrigger {
	return &ServiceTrigger{
		BaseDispatchTrigger: NewBaseDispatchTrigger("service", ch),
		ServiceWatcher:      watcher,
		prefix:              prefix,
	}
}

// NewServiceTrigger :
func NewServiceTrigger(ctx context.Context, client ClientAPI, prefix string, service string, tag string, ch chan *DispatchItem) (*ServiceTrigger, error) {
	watcher, err := NewServiceSnapshotWatcher(&WatcherConfig{
		Context: ctx,
		Client:  client,
	}, service, false)
	if err != nil {
		return nil, err
	}

	return NewServiceTriggerWithWatcher(watcher, prefix, ch), nil
}

// NewServiceTriggerCreator :
func NewServiceTriggerCreator(client ClientAPI, prefix string, service string, tag string) func(ctx context.Context, ch chan *DispatchItem) define.Task {
	return func(ctx context.Context, ch chan *DispatchItem) define.Task {
		trigger, err := NewServiceTrigger(ctx, client, prefix, service, tag, ch)
		if err != nil {
			panic(err)
		}
		return trigger
	}
}

// PairTrigger :
type PairTrigger struct {
	*BaseDispatchTrigger
	define.ServiceWatcher
	service define.Service
}

func (t *PairTrigger) activate(_ context.Context, service define.Service, pairs KVPairs) {
	logging.Debugf("service trigger activated")

	infos, err := service.Info(define.ServiceTypeAll)
	if err != nil {
		logging.Errorf("abort trigger activation because of error %v", err)
		return
	}

	t.Send(&DispatchItem{
		Pairs:    pairs,
		Services: infos,
	})
}

// Start :
func (t *PairTrigger) Start() error {
	err := t.ServiceWatcher.Start()
	if err != nil {
		return err
	}

	watcher := t.ServiceWatcher

	ctx, err := GetContextFromWatcher(watcher)
	if err != nil {
		return err
	}

	go func() {
	loop:
		for {
			select {
			case <-ctx.Done():
				break loop
			case ev := <-t.Events():
				pairs, ok := ev.Data.(KVPairs)
				if !ok {
					continue
				}
				t.activate(ctx, t.service, pairs)
			}
		}
	}()

	return nil
}

// NewPairTriggerWithWatcher :
func NewPairTriggerWithWatcher(watcher define.ServiceWatcher, service define.Service, ch chan *DispatchItem) *PairTrigger {
	return &PairTrigger{
		BaseDispatchTrigger: NewBaseDispatchTrigger("pair", ch),
		ServiceWatcher:      watcher,
		service:             service,
	}
}

// NewServiceTrigger
func NewPairTrigger(ctx context.Context, client ClientAPI, prefix string, service define.Service, ch chan *DispatchItem) (*PairTrigger, error) {
	watcher, err := NewPrefixSnapshotWatcher(&WatcherConfig{
		Context: ctx,
		Client:  client,
	}, prefix, false)
	if err != nil {
		return nil, err
	}

	return NewPairTriggerWithWatcher(watcher, service, ch), nil
}

// NewPairTriggerCreator :
func NewPairTriggerCreator(client ClientAPI, prefix string, service define.Service) func(ctx context.Context, ch chan *DispatchItem) define.Task {
	return func(ctx context.Context, ch chan *DispatchItem) define.Task {
		trigger, err := NewPairTrigger(ctx, client, prefix, service, ch)
		if err != nil {
			panic(err)
		}
		return trigger
	}
}

// PeriodTrigger
type PeriodTrigger struct {
	*BaseDispatchTrigger
	*define.PeriodTask
}

// NewPeriodTrigger
func NewPeriodTrigger(ctx context.Context, period time.Duration, client ClientAPI, prefix string, service define.Service, ch chan *DispatchItem) (*PeriodTrigger, error) {
	trigger := NewBaseDispatchTrigger("period", ch)
	task := define.NewPeriodTask(ctx, period, false, func(ctx context.Context) bool {
		api := client.KV()

		pairs, _, err := api.List(prefix, NewQueryOptions(ctx))
		if err != nil {
			logging.Warnf("list consul keys by %s error %v", prefix, err)
			return true
		}

		services, err := service.Info(define.ServiceTypeAll)
		if err != nil {
			logging.Warnf("list consul services error %v", err)
			return true
		}

		trigger.Send(&DispatchItem{
			Sender:   "period",
			Services: services,
			Pairs:    pairs,
		})
		return true
	})
	return &PeriodTrigger{
		BaseDispatchTrigger: trigger,
		PeriodTask:          task,
	}, nil
}

// NewPeriodTriggerCreator
func NewPeriodTriggerCreator(client ClientAPI, prefix string, service define.Service, period time.Duration) func(ctx context.Context, ch chan *DispatchItem) define.Task {
	return func(ctx context.Context, ch chan *DispatchItem) define.Task {
		trigger, err := NewPeriodTrigger(ctx, period, client, prefix, service, ch)
		if err != nil {
			panic(err)
		}
		return trigger
	}
}
