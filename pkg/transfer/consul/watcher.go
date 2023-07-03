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
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
)

// NewPlanByConfig
var NewPlanByConfig = func(conf *WatcherConfig) (WatchPlan, error) {
	plan, err := WatchPlanParseExempt(conf.Params, conf.Exempt)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return plan, nil
}

// WatcherConfig
type WatcherConfig struct {
	Context    context.Context
	Client     ClientAPI
	Type       string
	BufferSize int
	Params     map[string]interface{}
	Exempt     []string
	PreHandle  func(index uint64, value interface{}, ch chan<- *define.WatchEvent)
	Handler    func(index uint64, value interface{}, ch chan<- *define.WatchEvent)
	PostHandle func(index uint64, value interface{}, ch chan<- *define.WatchEvent)
	PreConvert func(evType define.WatchEventType, data interface{}) (interface{}, error)
	Converter  func(evType define.WatchEventType, data interface{}) (interface{}, error)
	PreSend    func(ev *define.WatchEvent, data interface{}) (*define.WatchEvent, error)
}

// Init
func (c *WatcherConfig) Init() *WatcherConfig {
	conf := c
	if conf == nil {
		conf = &WatcherConfig{}
	}
	if conf.Context == nil {
		conf.Context = context.Background()
	}
	if conf.Params == nil {
		conf.Params = make(map[string]interface{})
	}
	if conf.Exempt == nil {
		conf.Exempt = make([]string, 0)
	}
	return conf
}

// GetConfigFromWatcher
var GetConfigFromWatcher = func(watcher define.ServiceWatcher) (*WatcherConfig, error) {
	switch w := watcher.(type) {
	case *PlanWatcher:
		return w.config, nil
	default:
		return nil, errors.WithMessagef(define.ErrType, "%T not supported", w)
	}
}

// GetClientFromWatcher
var GetClientFromWatcher = func(watcher define.ServiceWatcher) (ClientAPI, error) {
	conf, err := GetConfigFromWatcher(watcher)
	if err != nil {
		return nil, err
	}
	return conf.Client, nil
}

// GetContextFromWatcher
var GetContextFromWatcher = func(watcher define.ServiceWatcher) (context.Context, error) {
	conf, err := GetConfigFromWatcher(watcher)
	if err != nil {
		return nil, err
	}
	return conf.Context, nil
}

// PlanWatcher
type PlanWatcher struct {
	*define.ContextTask
	config    *WatcherConfig
	plan      WatchPlan
	eventChan chan *define.WatchEvent
}

// NewPlanWatcher
func NewPlanWatcher(conf *WatcherConfig) (*PlanWatcher, error) {
	conf = conf.Init()
	conf.Params["type"] = conf.Type

	logging.Debugf("consul watcher created by params %v", conf.Params)

	// 此处会根据conf配置创建一个新的观察任务，实际上是对consul watcher的一个封装动作
	// 也是从这里会对conf的params有使用，对consul中的实例进行过滤查找
	plan, err := NewPlanByConfig(conf)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	preHandler := conf.PreHandle
	postHandler := conf.PostHandle
	handler := conf.Handler
	eventChan := make(chan *define.WatchEvent, conf.BufferSize)
	err = plan.SetHandler(func(u uint64, i interface{}) {
		if preHandler != nil {
			preHandler(u, i, eventChan)
		}
		handler(u, i, eventChan)
		if postHandler != nil {
			postHandler(u, i, eventChan)
		}
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return &PlanWatcher{
		config:    conf,
		plan:      plan,
		eventChan: eventChan,
		ContextTask: define.NewContextTask(conf.Context, func(ctx context.Context) {
			logging.Debugf("consul %s watcher start", conf.Type)
			err := plan.Run(conf.Client)
			if err != nil {
				logging.Warnf("consul watcher stop by error %v", err)
			}
			close(eventChan)
		}),
	}, nil
}

// Start
func (w *PlanWatcher) Start() error {
	err := w.ContextTask.Start()
	if err != nil {
		return err
	}

	return w.ContextTask.Activate(func(ctx context.Context) {
		<-ctx.Done()
		if !w.plan.IsStopped() {
			w.plan.Stop()
		}
	})
}

// Events
func (w *PlanWatcher) Events() <-chan *define.WatchEvent {
	return w.eventChan
}

func sendEvent(conf *WatcherConfig, ch chan<- *define.WatchEvent, id string, evType define.WatchEventType, data interface{}) (err error) {
	if conf.PreConvert != nil {
		data, err = conf.PreConvert(evType, data)
		if err != nil {
			return err
		}
	}

	result := data
	if conf.Converter != nil {
		result, err = conf.Converter(evType, data)
		if err != nil {
			return err
		}
	}

	event := &define.WatchEvent{
		ID:   id,
		Time: time.Now(),
		Type: evType,
		Data: result,
	}

	if conf.PreSend != nil {
		event, err = conf.PreSend(event, data)
		if err != nil {
			return err
		}
	}

	if event != nil {
		ch <- event
	}
	return nil
}

// NewKeySnapshotWatcher
func NewKeySnapshotWatcher(conf *WatcherConfig, target string, stale bool) (define.ServiceWatcher, error) {
	conf = conf.Init()
	conf.Type = "key"
	conf.Params = map[string]interface{}{
		"key":   target,
		"stale": stale,
	}
	conf.Handler = func(index uint64, value interface{}, ch chan<- *define.WatchEvent) {
		key := target
		pair, ok := value.(*KVPair)
		if !ok {
			return
		} else if pair != nil {
			key = pair.Key
		}

		err := sendEvent(conf, ch, key, define.WatchEventModified, pair)
		if err != nil {
			logging.Errorf("skip %#v because of error %v", pair, err)
		}
	}
	return NewPlanWatcher(conf)
}

// NewKeyDiffWatcher
func NewKeyDiffWatcher(conf *WatcherConfig, target string, stale bool) (define.ServiceWatcher, error) {
	conf = conf.Init()
	conf.Type = "key"
	conf.Params = map[string]interface{}{
		"key":   target,
		"stale": stale,
	}
	conf.Handler = func(index uint64, value interface{}, ch chan<- *define.WatchEvent) {
		var evType define.WatchEventType
		var key string

		pair, ok := value.(*KVPair)
		if pair == nil {
			evType = define.WatchEventDeleted
			key = target
		} else if !ok {
			return
		} else {
			key = pair.Key
			if pair.CreateIndex == pair.ModifyIndex {
				evType = define.WatchEventAdded
			} else {
				evType = define.WatchEventModified
			}
		}

		err := sendEvent(conf, ch, key, evType, pair)
		if err != nil {
			logging.Errorf("skip %#v because of error %v", pair, err)
		}
	}
	return NewPlanWatcher(conf)
}

// NewKeyPrefixWatcher watch keys which starts with prefix
func NewPrefixDiffWatcher(conf *WatcherConfig, prefix string, stale bool, withValue bool) (define.ServiceWatcher, error) {
	conf = conf.Init()
	// https://www.consul.io/docs/dynamic-app-config/watches#keyprefix
	conf.Type = "keyprefix"
	conf.Params = map[string]interface{}{
		"prefix": prefix, // 前缀匹配
		"stale":  stale,
	}

	// thread safety because of blocking query
	indexHelper := NewIndexHelper(true)
	conf.Handler = func(index uint64, value interface{}, ch chan<- *define.WatchEvent) {
		pairs, ok := value.(KVPairs)
		if !ok {
			return
		}

		indexHelper.Doer(func(id string, evType define.WatchEventType, data interface{}) {
			err := sendEvent(conf, ch, id, evType, data)
			if err != nil {
				logging.Errorf("skip %#v because of error %v", data, err)
			}
		})

		for _, pair := range pairs {
			if !withValue {
				pair.Value = nil
			}
			indexHelper.Update(pair.Key, pair.ModifyIndex, pair)

		}
		indexHelper.Rotate()
	}
	return NewPlanWatcher(conf)
}

// NewPrefixBatchDiffWatcher
func NewPrefixBatchDiffWatcher(conf *WatcherConfig, prefix string, stale bool, withValue bool) (define.ServiceWatcher, error) {
	conf = conf.Init()

	var events []*define.WatchEvent
	conf.PreHandle = func(index uint64, value interface{}, ch chan<- *define.WatchEvent) {
		events = make([]*define.WatchEvent, 0)
	}
	conf.PreSend = func(ev *define.WatchEvent, data interface{}) (event *define.WatchEvent, e error) {
		events = append(events, ev)
		return nil, nil
	}
	conf.PostHandle = func(index uint64, value interface{}, ch chan<- *define.WatchEvent) {
		ch <- &define.WatchEvent{
			ID:   prefix,
			Time: time.Now(),
			Type: define.WatchEventModified,
			Data: events,
		}
	}

	return NewPrefixDiffWatcher(conf, prefix, stale, withValue)
}

// NewPrefixSnapshotWatcher
func NewPrefixSnapshotWatcher(conf *WatcherConfig, prefix string, stale bool) (define.ServiceWatcher, error) {
	conf = conf.Init()
	conf.Type = "keyprefix"
	conf.Params = map[string]interface{}{
		"prefix": prefix,
		"stale":  stale,
	}
	conf.Handler = func(index uint64, value interface{}, ch chan<- *define.WatchEvent) {
		pairs, ok := value.(KVPairs)
		if !ok {
			return
		}

		err := sendEvent(conf, ch, prefix, define.WatchEventModified, pairs)
		if err != nil {
			logging.Errorf("skip %#v because of error %v", pairs, err)
		}
	}
	return NewPlanWatcher(conf)
}

// NewShadowPrefixDiffWatcher
func NewShadowPrefixDiffWatcher(conf *WatcherConfig, prefix string, stale bool) (define.ServiceWatcher, error) {
	conf = conf.Init()
	conf.PreConvert = func(evType define.WatchEventType, data interface{}) (i interface{}, e error) {
		shadowed := data.(*KVPair)
		payload := new(KVPair)
		err := json.Unmarshal(shadowed.Value, payload)
		if err != nil {
			return nil, errors.Wrapf(err, "load shadowed payload %s failed", shadowed.Value)
		}
		return payload, nil
	}

	return NewPrefixDiffWatcher(conf, prefix, stale, true)
}

// NewServiceSnapshotWatcher
func NewServiceSnapshotWatcher(conf *WatcherConfig, name string, stale bool) (define.ServiceWatcher, error) {
	prefix := path.Join(define.ConfRootV1, define.ConfClusterID, "session") + "/"
	conf = conf.Init()
	// https://www.consul.io/docs/dynamic-app-config/watches#keyprefix
	conf.Type = "keyprefix"
	conf.Params = map[string]interface{}{
		"prefix": prefix, // 前缀匹配
		"stale":  stale,
	}

	conf.Handler = func(index uint64, value interface{}, ch chan<- *define.WatchEvent) {
		kvs, _, err := conf.Client.KV().List(prefix, nil)
		if err != nil {
			logging.Errorf("list services from seesion kv failed %v", err)
			return
		}

		err = sendEvent(conf, ch, prefix, define.WatchEventModified, kvs)
		if err != nil {
			logging.Errorf("skip sent event because of error %v", err)
		}
	}

	conf.Converter = func(evType define.WatchEventType, data interface{}) (interface{}, error) {
		services := make([]*define.ServiceInfo, 0)
		kvs := data.(KVPairs)

		// 按 serviceID 进行分组
		groups := make(map[string]KVPairs)
		for _, kv := range kvs {
			spilt := strings.Split(strings.TrimSuffix(kv.Key, "/"), "/")
			service := spilt[len(spilt)-2]
			groups[service] = append(groups[service], kv)
		}

		for _, group := range groups {
			service, err := extractSessionDetailed(group)
			if err != nil {
				logging.Errorf("failed to extract service info from session: %v", err)
				continue
			}
			services = append(services, service)
		}
		return services, nil
	}

	return NewPlanWatcher(conf)
}
