// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cstockton/go-conv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/esb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	ConfRequestTypeHost = "host"
	ConfRequestTypeInst = "inst"
	ConfRequestTypeAll  = "all"
)

// CCHostUpdater :
type CCHostUpdater struct {
	updateSignalChan chan struct{}
	updateOnce       sync.Once             // 只更新一次
	cc               *esb.CCApiClient      // 连接cc 的client
	conf             define.Configuration  // 全局conf
	hostInfo         models.CCHostInfo     // 主机cache
	instanceInfo     models.CCInstanceInfo // 实例cache
	// 调度器的任务中所有拉取host，instance的动作 最终都会在此包内，通过updater触发，所以通过updater中的标志位控制。
	isRequestInst bool
	isRequestHost bool
}

// NewCCHostUpdater :
func NewCCHostUpdater(conf define.Configuration) *CCHostUpdater {
	// 判断要拉取的cmdb 缓存类型
	var (
		isRequestInst bool
		isRequestHost bool
		cacheType     = conf.GetString(ConfRequestTypeKey)
	)
	logging.Infof("request cmdb cache type: [%s]", cacheType)
	switch cacheType {
	case ConfRequestTypeInst:
		isRequestInst = true
		logging.Debug("set request instance true")
	case ConfRequestTypeAll:
		isRequestHost = true
		isRequestInst = true
		logging.Debug("set request instance true, request host true")
	default:
		isRequestHost = true
		logging.Debug("set request host true")
	}
	esbCli := esb.NewClient(conf)
	cc := esb.NewCCApiClient(esbCli)
	return &CCHostUpdater{
		conf: conf,
		cc:   cc,
		hostInfo: models.NewHostInfoWithTemplate(func() *models.CCTopoBaseModelInfo {
			return &models.CCTopoBaseModelInfo{}
		}),
		instanceInfo: models.NewInstanceInfoWithTemplate(func() *models.CCTopoBaseModelInfo {
			return &models.CCTopoBaseModelInfo{}
		}),
		updateSignalChan: make(chan struct{}, 1),
		isRequestHost:    isRequestHost,
		isRequestInst:    isRequestInst,
	}
}

func (c *CCHostUpdater) init(ctx context.Context) {
	go func() {
		defer utils.RecoverError(func(e error) {
			logging.Errorf("cc host updater panic: %+v", e)
		})

		var (
			expires       = c.conf.GetDuration(ConfSchedulerCCCacheExpires) // 过期时间
			expiresTicker *time.Ticker
			checkTicker   *time.Ticker
		)

		// 判断是否有超长的过期时间，导致缓存不能较好的定期更新，如果有，则需要配置为一个合理值
		if expires > time.Hour*1 {
			expiresTicker = time.NewTicker(time.Hour)
			checkTicker = time.NewTicker(time.Minute * 6)
			logging.Infof("expires set more than 1hr, will use default ticker config.")
		} else {
			expiresTicker = time.NewTicker(expires / 2)
			checkTicker = time.NewTicker(expires / 10) // 定期检查
		}

		// hostTotal 统计各个biz 下 host的数量，如果发现数量变化，那么要提前更新cache
		hostTotal := utils.MapHelper{Data: map[string]interface{}{}}
		instanceTotal := utils.MapHelper{Data: map[string]interface{}{}}
		bizTotal := 0
	loop:
		for {
			select {
			case <-ctx.Done():
				expiresTicker.Stop()
				checkTicker.Stop()
				break loop
			case <-expiresTicker.C:
				c.updateSignalChan <- struct{}{}
			case <-checkTicker.C:
				bizList, _ := c.cc.GetSearchBusiness()
				if bizList != nil && bizTotal != len(bizList) {
					bizTotal = len(bizList)
					c.updateSignalChan <- struct{}{}
					// 发送了更新信号后应该让 expiresTicker 重新计时
					expiresTicker = time.NewTicker(expires / 2)
				} else {
					var one sync.Once
					for _, bizID := range bizList {
						// 校验数据是否和内存中的数据相同，不同则触发更新
						if !c.checkAndSetData(hostTotal, instanceTotal, bizID) {
							one.Do(func() {
								c.updateSignalChan <- struct{}{}
								expiresTicker = time.NewTicker(expires / 2)
							})
						}
					}
				}
			}
		}
	}()
}

// checkAndSetData: true: 代表与内存中数量一致或gethost出错；false: 代表与内存中数据不同
func (c *CCHostUpdater) checkAndSetData(hostTotal, instanceTotal utils.MapHelper, bizID esb.CCSearchBusinessResponseInfo) bool {
	biz := conv.String(bizID.BKBizID)

	// 校验主机
	if c.isRequestHost {
		responseHost, err := c.cc.GetHostsByRange(bizID.BkTenantID, bizID.BKBizID, 1, 0)
		// 检测出错，则不触发更新动作
		if err != nil {
			logging.Errorf("host cache update periodically failed: %v", err)
			return true
		}
		// 内存中获取不到，或者数量对不上，则触发更新
		if value, ok := hostTotal.Get(biz); !ok || value != responseHost.Count {
			hostTotal.Set(biz, responseHost.Count)
			return false
		}
	}

	// 校验实例
	if c.isRequestInst {
		responseInstance, err := c.cc.GetServiceInstance(bizID.BkTenantID, bizID.BKBizID, 1, 0, nil)
		// 检测出错，则不触发更新动作
		if err != nil {
			logging.Errorf("instance cache update periodically failed: %v", err)
			return true
		}
		// 内存中获取不到，或者数量对不上，则触发更新
		if value, ok := instanceTotal.Get(biz); !ok || value != responseInstance.Count {
			instanceTotal.Set(biz, responseInstance.Count)
			return false
		}
	}

	return true
}

// NeedUpdate :
func (c *CCHostUpdater) NeedUpdate(ctx context.Context) bool {
	// 第一次进入时，将会触发一个初始化动作，将会产生一个goroutines定期产生需要更新的信号
	c.updateOnce.Do(func() {
		c.init(ctx)
	})
	needUpdate := false
loop:
	for {
		select {
		case <-c.updateSignalChan:
			needUpdate = true
		default:
			break loop
		}
	}
	return needUpdate
}

// UpdateTo : 更新CC缓存到本地存储当中
func (c *CCHostUpdater) UpdateTo(ctx context.Context, store define.Store) error {
	var (
		expires        = c.conf.GetDuration(ConfSchedulerCCCacheExpires)
		err            error
		hostUpdate     int64
		hostLost       int64
		instanceLost   int64
		instanceUpdate int64
		deDuplication  sync.Map
	)

	type tempCache = struct {
		HostKey string
		BizID   []int
		Topo    []map[string]string
	}

	t := time.Now()
	logging.Debugf("starting cc cache")
	loadStore := func(monitor esb.CCSearchHostResponseDataV3Monitor, ccInfo models.CCInfo) error {
		for _, value := range monitor.Info {
			hostInfo := models.NewHostInfoWithTemplate(func() *models.CCTopoBaseModelInfo {
				return &models.CCTopoBaseModelInfo{}
			})
			instanceInfo := models.NewInstanceInfoWithTemplate(func() *models.CCTopoBaseModelInfo {
				return &models.CCTopoBaseModelInfo{}
			})
			switch modelType := ccInfo.(type) {
			case *models.CCHostInfo:
				hostInfo.OuterIP = value.Host.BKOuterIP
				// 由于cmdb支持 hostInnerIp 使用","分隔配置多ip，但采集侧上报只使用一个ip，导致transfer清洗时将此采集数据drop
				// 当出现多ip的情况, 只取第一个
				ipList := strings.Split(value.Host.BKHostInnerIP, ",")
				if len(ipList) > 1 {
					logging.Infof("multi ip[%v] will be save as the first one", ipList)
				}

				hostInfo.CloudID = value.Host.BKCloudID
				hostInfo.IP = ipList[0]
				hostInfo.BizID = []int{value.BizID}
				hostInfo.Topo = value.Topo
				hostInfo.DbmMeta = value.Host.DbmMeta
				hostInfo.DevxMeta = value.Host.DevxMeta
				hostInfo.PerforceMeta = value.Host.PerforceMeta

				if v, ok := deDuplication.Load(hostInfo.GetStoreKey()); ok {
					if cache, ok := v.(tempCache); ok {
						// 利用deDuplication 做去重操作
						hostInfo.BizID = utils.RemoveRepByLoopInt(append(hostInfo.BizID, cache.BizID...))
						hostInfo.Topo = utils.RemoveRepByLoopMapString(append(hostInfo.Topo, cache.Topo...))
					}
				}
				deDuplication.Store(hostInfo.GetStoreKey(), tempCache{
					HostKey: hostInfo.GetStoreKey(),
					BizID:   hostInfo.BizID,
					Topo:    hostInfo.Topo,
				})
				logging.Debugf("[%s] host info before dump %v", hostInfo.IP, hostInfo.CCTopoBaseModelInfo)

				err = hostInfo.Dump(store, expires)
				if err != nil {
					logging.Errorf("unable to dump store %v", err)
					atomic.AddInt64(&hostLost, 1)
					continue
				}

				// 以缓存agent id为key，存储host信息
				if value.Host.BkAgentID != "" {
					hostAgentInfo := models.CCAgentHostInfo{
						AgentID: value.Host.BkAgentID,
						BizID:   value.BizID,
						IP:      value.Host.BKHostInnerIP,
						CloudID: value.Host.BKCloudID,
					}
					err = hostAgentInfo.Dump(store, expires)
					if err != nil {
						logging.Errorf("unable to dump store %v", err)
						atomic.AddInt64(&hostLost, 1)
						continue
					}

				}

				atomic.AddInt64(&hostUpdate, 1)

			case *models.CCInstanceInfo:
				instanceInfo.InstanceID = value.Host.BKHostInnerIP
				instanceInfo.BizID = []int{value.BizID}
				instanceInfo.Topo = value.Topo
				if v, ok := deDuplication.Load(instanceInfo.GetStoreKey()); ok {
					if cache, ok := v.(tempCache); ok {
						instanceInfo.BizID = utils.RemoveRepByLoopInt(append(instanceInfo.BizID, cache.BizID...))
						instanceInfo.Topo = utils.RemoveRepByLoopMapString(append(instanceInfo.Topo, cache.Topo...))
					}
				}
				deDuplication.Store(instanceInfo.GetStoreKey(), tempCache{
					HostKey: instanceInfo.GetStoreKey(),
					BizID:   instanceInfo.BizID,
					Topo:    instanceInfo.Topo,
				})
				err = instanceInfo.Dump(store, expires)

				if err != nil {
					logging.Errorf("unable to dump store %v", err)
					atomic.AddInt64(&instanceLost, 1)
					continue
				}
				atomic.AddInt64(&instanceUpdate, 1)
			default:
				return fmt.Errorf("unexpect model type: %+v", modelType)
			}

			if err != nil {
				return err
			}
		}
		return nil
	}

	var instanceErr error
	var hostErr error
	if c.isRequestInst {
		instanceErr = c.cc.VisitAllHost(ctx, c.conf.GetInt(ConfSchedulerCCBatchSize), &c.instanceInfo, loadStore)
		if instanceErr != nil {
			logging.Fatalf("cc instance cache error by %v", err)
		}
	}

	if c.isRequestHost {
		hostErr = c.cc.VisitAllHost(ctx, c.conf.GetInt(ConfSchedulerCCBatchSize), &c.hostInfo, loadStore)
		if err != nil {
			logging.Fatalf("cc host cache error by %v", err)
		}
	}

	logging.Infof("updated %d cc host info", hostUpdate)
	logging.Infof("updated %d cc instance info", instanceUpdate)
	// 统计cc cache 耗时
	logging.Infof("caching cc data cost %v totally", time.Since(t))

	if instanceErr != nil {
		return instanceErr
	} else if hostErr != nil {
		return hostErr
	}

	return nil
}

// NewCCHostUpdateTask :
func NewCCHostUpdateTask(ctx context.Context, conf define.Configuration) define.Task {
	// CC缓存更新时间间隔
	period := conf.GetDuration(ConfSchedulerCCCheckIntervalKey)
	// CC缓存超时时间
	flagExpires := conf.GetDuration(ConfSchedulerCCCacheExpires) - period
	// 获取一个CC更新方法client句柄
	updater := NewCCHostUpdater(conf)
	// 通过配置文件，得到一个持久化的配置句柄
	store := define.StoreFromContext(ctx)

	// 注册多个事件
	// 存储写入时间
	logging.PanicIf(eventbus.SubscribeAsync(eventbus.EvSigCommitCache, func(params map[string]string) {
		logging.Warnf("commit cache by signal")
		logging.WarnIf("commit store error", store.Commit())
	}, false))

	logging.PanicIf(eventbus.SubscribeAsync(eventbus.EvSigUpdateCCWorker, func(params map[string]string) {
		esb.MaxWorkerConfig = conv.Int(params["max_worker"])
		logging.Warnf("update max_worker")
	}, false))

	// 注册update-cc-cache信号，由外部强行更新缓存事件
	logging.PanicIf(eventbus.SubscribeAsync(eventbus.EvSigUpdateCCCache, func(params map[string]string) {
		logging.Infof("update cc cache by signal in %v", period)
		updater.updateSignalChan <- struct{}{}
	}, false))

	// 注册dump-host-info信号，有外部触发导出当前CC缓存内容
	logging.PanicIf(eventbus.SubscribeAsync(eventbus.EvSigDumpHostInfo, func(params map[string]string) {
		var host models.CCHostInfo
		logging.Infof("ready to dump cc cached hosts")
		logging.PanicIf(store.Scan(models.HostInfoStorePrefix, func(key string, data []byte) bool {
			err := host.LoadByBytes(data)
			if err != nil {
				logging.Warnf("load host %s error: %v", key, err)
			} else {
				info, err := json.Marshal(host)
				if err != nil {
					logging.Infof("load host %s details: %#v", key, host)
				} else {
					logging.Infof("load host %s details: %#v", key, conv.String(info))
				}
			}
			return true
		}))
	}, false))
	// 注册dump-instance-info信号
	logging.PanicIf(eventbus.SubscribeAsync(eventbus.EvSigDumpInstanceInfo, func(params map[string]string) {
		logging.PanicIf(store.Scan(models.InstanceInfoStorePrefix, func(key string, data []byte) bool {
			var instance models.CCInstanceInfo

			err := instance.LoadByBytes(data)
			if err != nil {
				logging.Warnf("load instance %s error: %v", key, err)
			} else {
				info, err := json.Marshal(instance)
				if err != nil {
					logging.Infof("load instance %s details: %#v", key, instance)
				} else {
					logging.Infof("load instance %s details: %#v", key, conv.String(info))
				}
			}
			return true
		}))
	}, false))

	var once sync.Once
	flag := define.StoreFlag

	// 注册一个定时任务，周期新的更新CC缓存
	// 定时任务为阻塞式任务，前一个任务完成后进行下一个任务的周期计算
	return define.NewPeriodTask(ctx, period, true, func(subCtx context.Context) bool {
		// 首次触发时，将会先发出一个更新的信号
		once.Do(func() {
			logging.Infof("First update cmdb cache")
			ok, err := store.Exists(flag)
			if err != nil {
				logging.Warnf("get flag %s error %v", flag, err)
				ok = false
			}
			if !ok {
				logging.Infof("activating cc cache in boot")
				updater.updateSignalChan <- struct{}{}
			}
		})

		// 判断是否需要更新
		if updater.NeedUpdate(subCtx) {
			err := updater.UpdateTo(subCtx, store)
			if err != nil {
				logging.Errorf("update cc info error %v", err)
				return true
			}

			logging.Debugf("updating flag cc cache %s with ttl %v", flag, flagExpires)
			err = store.Set(flag, []byte("x"), flagExpires)
			if err != nil {
				logging.Warnf("store flag %s error %v", flag, err)
			}
			logging.WarnIf("commit store error", store.Commit())
		}
		return true
	})
}
