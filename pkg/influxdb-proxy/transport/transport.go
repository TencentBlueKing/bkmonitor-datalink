// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package transport

import (
	"context"
	"fmt"
	"sync"
	"time"

	client "github.com/influxdata/influxdb1-client/v2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

var moduleName = "transport"

// Transport :
type Transport struct {
	ctx            context.Context
	clientMap      map[string]client.Client
	queryDuration  string
	maxQueryLines  int
	writeBatchSize int
}

// NewTransport :
func NewTransport(ctx context.Context, queryDuration string, maxQueryLines int, writeBatchSize int) *Transport {
	trans := &Transport{
		ctx:            ctx,
		queryDuration:  queryDuration,
		maxQueryLines:  maxQueryLines,
		writeBatchSize: writeBatchSize,
	}
	return trans
}

// 获取backend实例
func (t *Transport) getClientInstance(ctx context.Context, name string, hostInfo *consul.HostInfo) (client.Client, error) {
	address := fmt.Sprintf("http://%s:%d", hostInfo.DomainName, hostInfo.Port)
	if hostInfo.Protocol == "https" {
		address = fmt.Sprintf("https://%s:%d", hostInfo.DomainName, hostInfo.Port)
	}
	cli, err := GetClient(address, hostInfo.Username, hostInfo.Password)
	return cli, err
}

func (t *Transport) makeMergePlan(clusterName, tagsKey string, tagInfo *consul.TagInfo) (*consul.TagInfo, error) {
	logger := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	sourceBackends := tagInfo.HostList
	var timestamp int64
	var err error
	db, measurement, tags := common.AnaylizeTagsKey(tagsKey)
	// 1.获取最早时间戳
	for _, backendName := range sourceBackends {
		backend, ok := t.clientMap[backendName]
		if !ok {
			logger.Errorf("get backend by name failed,name:%s", backendName)
			continue
		}
		timestamp, err = QueryTimestamp(db, measurement, tags, backend)
		if err != nil {
			logger.Errorf("query timestamp error:%s", err)
			continue
		}
	}
	if timestamp == 0 {
		return nil, ErrGetTimestampFailed
	}

	// 2.获取当前时间戳
	// 获取现在的时间戳,向前加一天容错
	now := time.Now().Add(24 * time.Hour).Unix()
	// 生成新的tagInfo
	newTagInfo := &consul.TagInfo{
		HostList:          tagInfo.HostList,
		DeleteHostList:    tagInfo.DeleteHostList,
		UnreadableHost:    tagInfo.UnreadableHost,
		Status:            StatusMerging,
		TransportStartAt:  timestamp,
		TransportLastAt:   timestamp,
		TransportFinishAt: now,
	}

	// 3.将信息更新到consul
	err = consul.ModifyTagInfo(clusterName, tagsKey, tagInfo, newTagInfo)
	if err != nil {
		return nil, err
	}
	return newTagInfo, nil
}

func (t *Transport) finishMerge(clusterName, tagsKey string, tagInfo *consul.TagInfo) error {
	hostList := tagInfo.HostList
	delList := tagInfo.DeleteHostList
	addList := tagInfo.UnreadableHost
	newHostList := make([]string, 0)
	for _, host := range hostList {
		exist := false
		for _, delHost := range delList {
			if delHost == host {
				exist = true
			}
		}
		if !exist {
			newHostList = append(newHostList, host)
		}
	}
	for _, host := range addList {
		newHostList = append(newHostList, host)
	}
	// 生成新的info，更新lastat
	changedTagInfo := &consul.TagInfo{
		HostList: newHostList,
		Status:   StatusReady,
	}
	err := consul.ModifyTagInfo(clusterName, tagsKey, tagInfo, changedTagInfo)
	if err != nil {
		return err
	}
	return nil
}

func (t *Transport) queryFromClient(db, measurement string, tags common.Tags, start, end int64, tagInfo *consul.TagInfo, ch chan<- client.BatchPoints) error {
	logger := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	sourceBackends := tagInfo.HostList
	send := false
	for _, sourceName := range sourceBackends {
		backend, ok := t.clientMap[sourceName]
		if !ok {
			logger.Warnf("get backend:%s,failed", sourceName)
			continue
		}
		err := t.queryClientToWriteData(db, measurement, tags, start, end, backend, ch)
		if err != nil {
			if err == ErrQueryOverflow {
				return err
			}
			logger.Warnf("query failed,backend:%s,error:%s", sourceName, err)
			continue
		}
		send = true
	}
	if !send {
		return ErrQueryFailed
	}

	return nil
}

func (t *Transport) sendIntoClient(db string, tagInfo *consul.TagInfo, ch <-chan client.BatchPoints) error {
	logger := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	targetBackends := tagInfo.UnreadableHost
	for points := range ch {
		logger.Debugf("send %d lines", len(points.Points()))
		for _, targetName := range targetBackends {
			backend, ok := t.clientMap[targetName]
			if !ok {
				logger.Warnf("get target backend:%s failed", targetName)
				continue
			}
			err := WriteClient(db, points, backend)
			if err != nil {
				// TODO 写入失败有多种情况，需要考虑针对特定情况过滤错误
				logger.Warnf("write to backend failed,error:%s", err)
				continue
			}
		}
	}
	return nil
}

func (t *Transport) mergeData(db, measurement string, tags common.Tags, start, end int64, tagInfo *consul.TagInfo) error {
	logger := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	wg := new(sync.WaitGroup)
	ch := make(chan client.BatchPoints)
	var globalError error

	// receiver
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := t.sendIntoClient(db, tagInfo, ch)
		if err != nil {
			logger.Errorf("send into client failed,error:%s", err)
			globalError = err
		}
	}()

	// sender
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(ch)
		err := t.queryFromClient(db, measurement, tags, start, end, tagInfo, ch)
		if err != nil {
			logger.Errorf("query from client failed,error:%s", err)
			globalError = err
		}
	}()

	wg.Wait()
	if globalError != nil {
		return globalError
	}

	return nil
}

// MergeData 数据迁移方法
func (t *Transport) MergeData(sessionID, clusterName, tagsKey string, tagInfo *consul.TagInfo) error {
	logger := logging.NewEntry(map[string]interface{}{
		"module":    moduleName,
		"sessionID": sessionID,
		"cluster":   clusterName,
		"tags":      tagsKey,
	})
	// ready状态说明不需要迁移
	if tagInfo.Status == StatusReady {
		logger.Debugf("transport found ready status, skip it")
		return nil
	}

	logger.Info("found not ready tags, try to get lock")
	// 这里要占个锁,占锁失败则跳过该tag的处理
	success, err := consul.GetTagItemLock(sessionID, clusterName, tagsKey)
	if err != nil {
		logger.Errorf("get lock error:%s", err)
		return err
	}
	// 如果占锁失败，说明有别的进程占用
	if !success {
		logger.Infof("another transport get lock,skip")
		return nil
	}
	// 处理结束时才释放锁
	defer consul.ReleaseTagItemLock(sessionID, clusterName, tagsKey)

	logger.Infof("start to merge data")

	// 如果没有目标迁移机器，则将状态直接改为ready
	if len(tagInfo.UnreadableHost) == 0 {
		logger.Info("no target to merge,change to ready")
		err := t.finishMerge(clusterName, tagsKey, tagInfo)
		if err != nil {
			logger.Errorf("merge finished, but change info failed,error:%s", err)
			return err
		}
		return nil
	}

	// 	如果状态是changed，需要先做好迁移规划
	if tagInfo.Status == StatusChanged {
		logger.Infof("start to make plan")
		tagInfo, err = t.makeMergePlan(clusterName, tagsKey, tagInfo)
		if err != nil {
			logger.Errorf("make merge plan failed,err:%s", err)
			return err
		}
	}
	// 默认单次迁移数据的查询时间长度，该参数影响到实际influxdb在迁移时会受到的压力
	queryDuration, err := time.ParseDuration(t.queryDuration)
	if err != nil {
		logger.Errorf("parse query duration failed,err:%s", err)
		return err
	}

	// 根据tagKey解析数据
	db, measurement, tags := common.AnaylizeTagsKey(tagsKey)

	for {
		// 从last开始
		start := tagInfo.TransportLastAt
		before, err := time.ParseDuration("-10m")
		if err != nil {
			return err
		}
		// 向后延长两个小时，以确保不在时间边界漏数据
		endTime := time.Unix(start, 0).Add(queryDuration)
		end := endTime.Unix()
		// 向前延长10分钟，以确保不在时间边界漏数据
		startTime := time.Unix(start, 0).Add(before)
		start = startTime.Unix()
		// 如果时间超过了，就选择标记的位置结束
		if end > tagInfo.TransportFinishAt {
			end = tagInfo.TransportFinishAt
		}
		logger.Infof("merging data from->[%s] to->[%s]", startTime, endTime)
		// 开始迁移数据
		err = t.mergeData(db, measurement, tags, start, end, tagInfo)
		if err != nil {
			// 针对查询到过多数据的处理，查询时间区间减半之后再查询
			if err == ErrQueryOverflow {
				queryDuration = queryDuration / 2
				// 周期太小还是报错吧，避免出现死循环
				if queryDuration < 1*time.Minute {
					logger.Errorf("query duration->[%s] is too small,query is stop,maxlines should be bigger maybe?", queryDuration)
					return ErrTooSmallDuration
				}
				logger.Warnf("merging data failed for too much data,duration cut half to:%s", queryDuration)
				continue
			}
			logger.Errorf("merging data failed,error:%s", err)
			return err
		}
		logger.Infof("merging data from->[%s] to->[%s] success", startTime, endTime)

		// 如果最后一次迁移任务追上了finish，判断迁移结束,退出循环
		if end >= tagInfo.TransportFinishAt {
			logger.Info("merge finished,change consul info")
			err := t.finishMerge(clusterName, tagsKey, tagInfo)
			if err != nil {
				logger.Errorf("merge finished, but change info failed,error:%s", err)
				return err
			}
			break
		}
		// 否则更新迁移进度，进行下一次循环
		changedTagInfo := &consul.TagInfo{
			HostList:          tagInfo.HostList,
			DeleteHostList:    tagInfo.DeleteHostList,
			UnreadableHost:    tagInfo.UnreadableHost,
			Status:            tagInfo.Status,
			TransportStartAt:  tagInfo.TransportStartAt,
			TransportLastAt:   end,
			TransportFinishAt: tagInfo.TransportFinishAt,
		}
		err = consul.ModifyTagInfo(clusterName, tagsKey, tagInfo, changedTagInfo)
		if err != nil {
			logger.Errorf("modify consul info failed,error:%s", err)
			return err
		}
		logger.Info("consul info updated,start next merge")
		tagInfo = changedTagInfo
	}

	err = consul.NotifyTagChanged(clusterName)
	if err != nil {
		logger.Errorf("merge done but notify failed,error:%s", err)
		return err
	}
	logger.Infof("transport merge done cluster:%s,tags:%s", clusterName, tagsKey)
	return nil
}

// RefreshClients 从consul读取并刷新主机实例
func (t *Transport) RefreshClients(ctx context.Context) error {
	logger := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	hostsData, err := consul.GetAllHostsData()
	if err != nil {
		logger.Errorf("get consul host data failed,error:%s", err)
		return err
	}
	clientMap := make(map[string]client.Client)
	for name, hostInfo := range hostsData {
		cli, err := t.getClientInstance(ctx, name, hostInfo)
		if err != nil {
			logger.Errorf("get client instance:%s", err)
			return err
		}
		clientMap[name] = cli
	}
	t.clientMap = clientMap

	return nil
}

// CheckTagInfos 检查tag数据，如果存在需要迁移的数据，则开始迁移
func (t *Transport) CheckTagInfos(sessionID string) error {
	logger := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	logger.Debugf("period check start")
	ctx, cancel := context.WithCancel(t.ctx)
	defer cancel()

	err := t.RefreshClients(ctx)
	if err != nil {
		logger.Errorf("refresh client failed,error:%s", err)
		return err
	}
	clusterData, err := consul.GetAllClustersData()
	if err != nil {
		logger.Errorf("get cluster data failed,error:%s", err)
		return err
	}
	wg := new(sync.WaitGroup)
	// 遍历cluster数据
	for clusterName := range clusterData {
		tagsInfos, err := consul.GetTagsInfo(clusterName)
		if err != nil {
			logger.Errorf("get tags data failed,error:%s", err)
			return err
		}
		for tagsKey, tagsInfo := range tagsInfos {
			// 跳过默认路由，这个无法迁移
			if tagsKey == "__default__/__default__/__default__==__default__" {
				continue
			}
			logger.Debugf("start check cluster:%s,tags:%s", clusterName, tagsKey)
			// 并发启动迁移
			wg.Add(1)
			go func(sessionID, clusterName, tagsKey string, tagsInfo *consul.TagInfo) {
				defer wg.Done()
				// 根据处理后的tagInfo开始迁移数据
				err := t.MergeData(sessionID, clusterName, tagsKey, tagsInfo)
				if err != nil {
					logger.Errorf("merge data failed,error:%s", err)
					return
				}
				logger.Debugf("check done cluster:%s,tags:%s", clusterName, tagsKey)
			}(sessionID, clusterName, tagsKey, tagsInfo)
		}
	}
	wg.Wait()
	logger.Debugf("all cluster check done")
	return nil
}
