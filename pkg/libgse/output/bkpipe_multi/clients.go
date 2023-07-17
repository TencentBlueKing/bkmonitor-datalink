// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkpipe_multi

import (
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/libbeat/outputs"
	"sync"
	"time"
)

const EventTaskIDMetaFieldName = "bk_task_id"

const GseOutputGroupName = "default"

type output struct {
	config            common.ConfigNamespace
	client            outputs.Client
	retryBackoff      *backoff.ExponentialBackOff
	lastRetryTime     time.Time
	nextRetryDuration time.Duration
}

// taskRegistry 任务注册表。key 为任务ID，value 为任务发送配置的计算哈希值
var taskRegistry = map[string]string{}

// outputRegistry 发送配置注册表。key 为任务发送配置的计算哈希值，value 为具体配置内容及生成的客户端对象
var outputRegistry = map[string]*output{}

var loadMutex sync.Mutex

// RegisterTaskOutput 按任务ID注册发送配置
func RegisterTaskOutput(taskID string, config common.ConfigNamespace) error {
	configHash, err := HashRawConfig(config)
	if err != nil {
		return err
	}
	taskRegistry[taskID] = configHash

	_, ok := taskRegistry[configHash]

	if ok {
		return nil
	}

	// 初始化指数后退对象
	bo := backoff.ExponentialBackOff{
		InitialInterval:     1 * time.Second,     // 初始间隔
		RandomizationFactor: 0.5,                 // 随机因子
		Multiplier:          2,                   // 乘数
		MaxInterval:         60 * time.Second,    // 最大间隔
		MaxElapsedTime:      0,                   // 最大重试时间，0代表一直重试
		Clock:               backoff.SystemClock, // 使用系统时钟
	}
	bo.Reset()

	outputRegistry[configHash] = &output{
		config:       config,
		retryBackoff: &bo,
	}

	return nil
}

// LoadOutputClient 根据任务发送配置的哈希值，获取发送客户端对象
func LoadOutputClient(
	configHash string,
	im outputs.IndexManager,
	info beat.Info,
	stats outputs.Observer,
) (outputs.Client, error) {

	loadMutex.Lock()
	defer loadMutex.Unlock()

	out, ok := outputRegistry[configHash]

	if !ok {
		return nil, fmt.Errorf("output config hash does not registered: %s", configHash)
	}

	if out.client == nil {

		// 指数后退重试判断，避免短时间内多次重试导致不合理的资源占用
		elaspedTime := time.Since(out.lastRetryTime)
		if elaspedTime < out.nextRetryDuration {
			//return nil, fmt.Errorf("client: %s, retry time not reached, remaining: %s", out.config.Name(), out.nextRetryDuration-elaspedTime)
			return nil, nil
		}
		out.nextRetryDuration = out.retryBackoff.NextBackOff()
		out.lastRetryTime = time.Now()

		// 如果客户端尚未初始化，则需要走初始化流程
		group, err := outputs.Load(im, info, stats, out.config.Name(), out.config.Config())
		if err != nil {
			return nil, err
		}
		outClient := group.Clients[0]
		networkClient, ok := outClient.(outputs.NetworkClient)

		if ok {
			// 如果 output 实现了 NetworkClient 则需要调用其 Connect 接口
			if err = networkClient.Connect(); err != nil {
				return nil, fmt.Errorf("failed to connect to %s: %v", out.config.Name(), err)
			}
		}
		out.client = outClient

		// 一旦重试成功，则重置 backoff 计数器
		out.retryBackoff.Reset()
	}

	return out.client, nil
}

// SetEventTaskID 为事件注入任务ID属性，用于后续路由到正确的发送端
func SetEventTaskID(event beat.Event, taskID string) beat.Event {
	if event.Meta == nil {
		event.Meta = common.MapStr{
			EventTaskIDMetaFieldName: taskID,
		}
	} else {
		event.Meta[EventTaskIDMetaFieldName] = taskID
	}
	return event
}

// GroupEventsByOutput 根据事件中的任务ID，按发送端类型进行分组
func GroupEventsByOutput(events []beat.Event) map[string][]beat.Event {
	groups := make(map[string][]beat.Event)

	for i := range events {
		groupName := GseOutputGroupName

		event := events[i]
		taskID, ok := event.Meta[EventTaskIDMetaFieldName].(string)

		if !ok {
			// Meta字段中没找到任务ID字段，走默认上报
			groups[groupName] = append(groups[groupName], event)
			continue
		}

		configHash, ok := taskRegistry[taskID]
		if ok {
			// Meta字段中有任务字段，并且有对应的 Output 记录，则使用 Output 配置的哈希值作为 key
			groupName = configHash
			// 任务ID用完后即清除，避免上报上去
			delete(event.Meta, EventTaskIDMetaFieldName)
		}

		groups[groupName] = append(groups[groupName], event)

	}
	return groups
}

// CloseOutputClients 关闭所有已初始化的 Output 客户端连接
func CloseOutputClients() {
	for _, out := range outputRegistry {
		if out.client == nil {
			continue
		}
		// 忽略错误
		_ = out.client.Close()
		out.client = nil
	}
}
