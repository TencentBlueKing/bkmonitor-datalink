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
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

var (
	// TagBasePath tag路由基础路径
	TagBasePath = "tag_info"
	// TagPath  :
	TagPath     string
	TagLockPath string
)

func initTagPath() {
	TagPath = TotalPrefix + "/" + TagBasePath
	TagLockPath = LockPath + "/" + TagBasePath
}

func formatTagPath(cluster, path string) string {
	tagKey := strings.Replace(path, TagPath+"/"+cluster+"/", "", 1)
	tagKey = strings.TrimSuffix(tagKey, "/")
	return tagKey
}

// GetTagsInfo 根据集群名，获取所有现存的tag配置
var GetTagsInfo = func(cluster string) (map[string]*TagInfo, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	prefix := TotalPrefix + "/" + TagBasePath
	// 不传就查询所有的tag，否则查询指定集群的tag
	if cluster != "" {
		prefix = prefix + "/" + cluster
	}
	data, err := consulClient.GetPrefix(TotalPrefix+"/"+TagBasePath+"/"+cluster, "/")
	if err != nil {
		return nil, err
	}
	tagMap := make(map[string]*TagInfo)
	for _, kvPair := range data {

		tagKey := formatTagPath(cluster, kvPair.Key)
		// 跳过version触发机制
		if tagKey == "version" {
			continue
		}
		ti, err := kvToTagInfo(kvPair)
		if err != nil {
			flowLog.Errorf("get tag info by kv failed")
			return nil, err
		}

		tagMap[tagKey] = ti
	}
	flowLog.Tracef("done")
	return tagMap, nil
}

// AddTagInfo 添加一个新的tag信息到consul上
var AddTagInfo = func(cluster string, key string, info *TagInfo) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	path := TagPath + "/" + cluster + "/" + key
	value, err := json.Marshal(info)
	if err != nil {
		return err
	}
	result, err := consulClient.CAS(path, nil, value)
	if err != nil {
		return err
	}
	if result == false {
		return ErrTagInfoChanged
	}
	flowLog.Infof("add tag info success,key:%s,value:%s", key, value)
	return nil
}

// ModifyTagInfo 修改tagInfo 依然使用cas
var ModifyTagInfo = func(cluster string, key string, oldInfo, newInfo *TagInfo) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	path := TagPath + "/" + cluster + "/" + key
	oldValue, err := json.Marshal(oldInfo)
	if err != nil {
		return err
	}
	newValue, err := json.Marshal(newInfo)
	if err != nil {
		return err
	}
	flowLog.Debugf("start cas tag info,key:%s,old:%s,new:%s", key, oldValue, newValue)
	result, err := consulClient.CAS(path, oldValue, newValue)
	if err != nil {
		return err
	}
	if result == false {
		return ErrTagInfoChanged
	}
	flowLog.Debugf("modify tag info success,key:%s,old:%s,new:%s", key, oldValue, newValue)

	return nil
}

// NotifyTagChanged :
var NotifyTagChanged = func(cluster string) error {
	watchPath := TagPath + "/" + cluster + "/" + "version" + "/"
	now := time.Now().Unix()
	nowTime := strconv.FormatInt(now, 10)
	return consulClient.Put(watchPath, []byte(nowTime))
}

// WatchTagChange :
var WatchTagChange = func(ctx context.Context, cluster string) (<-chan string, error) {
	watchPath := TagPath + "/" + cluster + "/" + "version" + "/"
	return WatchChange(ctx, watchPath, []string{})
}

// NewSession 获取一个新的session，过期时间60s，每15s刷新
var NewSession = func(ctx context.Context) (string, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	sessionID, err := consulClient.NewSessionID("60s")
	if err != nil {
		return "", err
	}
	// 启动定时器，定时刷新session，直到ctx.Done()
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				err := consulClient.RenewSession(sessionID)
				if err != nil {
					flowLog.Errorf("refresh session failed sessionID:%s,error:%s", sessionID, err)
				}
			}
		}
	}()
	return sessionID, nil
}

// GetTagLock 获取tag全局锁
var GetTagLock = func(sessionID string) (bool, error) {
	lockPath := TagLockPath
	success, err := consulClient.Acquire(lockPath, sessionID)
	if err != nil {
		return false, err
	}
	return success, err
}

// ReleaseTagLock 释放tag全局锁
var ReleaseTagLock = func(sessionID string) (bool, error) {
	lockPath := TagLockPath
	success, err := consulClient.Release(lockPath, sessionID)
	if err != nil {
		return false, err
	}
	return success, nil
}

// GetTagItemLock 获取tag子锁
var GetTagItemLock = func(sessionID, cluster, tagsKey string) (bool, error) {
	lockPath := TagLockPath + "/" + cluster + "/" + tagsKey
	success, err := consulClient.Acquire(lockPath, sessionID)
	if err != nil {
		return false, err
	}
	return success, err
}

// ReleaseTagItemLock 释放tag子锁
var ReleaseTagItemLock = func(sessionID, cluster, tagsKey string) (bool, error) {
	lockPath := TagLockPath + "/" + cluster + "/" + tagsKey
	success, err := consulClient.Release(lockPath, sessionID)
	if err != nil {
		return false, err
	}
	return success, nil
}
