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
	"fmt"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// ConsClient provides a wrapper around the consulkv Client
type ConsClient struct {
	client          *api.KV
	ctx             context.Context
	eventBufferSize int
}

var NewConsulAPIClient = func(ctx context.Context) (*api.Client, error) {
	conf := config.FromContext(ctx)
	if conf == nil {
		conf = config.Configuration
	}

	cfg := NewConsulConfigFromConfig(conf)

	client, err := api.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// NewConsulClient returns a new Client to Consul for the given Address
var NewConsulClient = func(ctx context.Context) (SourceClient, error) {
	conf := config.FromContext(ctx)
	if conf == nil {
		conf = config.Configuration
	}

	cfg := NewConsulConfigFromConfig(conf)

	client, err := api.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &ConsClient{
		client:          client.KV(),
		ctx:             ctx,
		eventBufferSize: conf.GetInt(ConfKeyEventBufferSize),
	}, nil
}

// SetContext :
func (c *ConsClient) SetContext(ctx context.Context) {
	c.ctx = ctx
}

// Put :
func (c *ConsClient) Put(key string, value []byte) error {
	p := &api.KVPair{Key: key, Value: value}
	_, err := c.client.Put(p, nil)
	return err
}

func (c *ConsClient) GetKeys(prefix string) ([]string, error) {
	result, _, err := c.client.Keys(prefix, "/", nil)
	return result, err
}

// KeepSession : create a serviceSession and renew it every ttl/2
func (c *ConsClient) KeepSession() (string, error) {
	cfg := config.FromContext(c.ctx)
	conf := NewConsulConfigFromConfig(cfg)
	sessionTTL := "10s"
	sessionID := "monitor_transfer_id"
	sessionName := "monitor_transfer_name"
	if cfg != nil {
		conf.Address = fmt.Sprintf("%s:%d", cfg.GetString(ConfKeyHost), cfg.GetInt(ConfKeyPort))
		sessionTTL = cfg.GetString(ConfKeyClientTTL)
		sessionID = cfg.GetString(ConfKeyClientID)
		sessionName = cfg.GetString(ConfKeyServiceName)
	}
	client, err := api.NewClient(conf)
	if err != nil {
		return "", err
	}
	session := client.Session()
	id, _, err := session.Create(&api.SessionEntry{ID: sessionID, Name: sessionName, Behavior: api.SessionBehaviorDelete, TTL: sessionTTL}, nil)
	if err != nil {
		return "", err
	}
	d := make(chan struct{})
	go func() {
		if err := session.RenewPeriodic(sessionTTL, id, nil, d); err != nil {
			logging.Errorf("serviceSession RenewPeriodic failed:%s", err.Error())
		}
	}()
	return id, nil
}

// DestroySession : destroy a serviceSession
func (c *ConsClient) DestroySession(sessionID string) error {
	cfg := config.FromContext(c.ctx)
	conf := NewConsulConfigFromConfig(cfg)
	client, err := api.NewClient(conf)
	if err != nil {
		return err
	}
	session := client.Session()
	_, err = session.Destroy(sessionID, nil)
	return err
}

// CreateTempNode : create a temporary node
func (c *ConsClient) CreateTempNode(key, value, sessionID string) error {
	p := &api.KVPair{Key: key, Value: []byte(value), Session: sessionID}
	_, err := c.client.Put(p, nil)
	if err != nil {
		return err
	}

	res, _, err := c.client.Acquire(p, nil)
	if !res || err != nil {
		return fmt.Errorf("acquire consul node failed:%s", err.Error())
	}
	return err
}

// Delete :
func (c *ConsClient) Delete(key string) error {
	_, err := c.client.Delete(key, nil)
	if err != nil {
		return err
	}
	return nil
}

// Get :
func (c *ConsClient) Get(key string) ([]byte, error) {
	pair, _, err := c.client.Get(key, nil)
	if err != nil {
		return nil, err
	}
	if pair == nil {
		return nil, errors.Wrapf(define.ErrItemNotFound, "key %s not exist", key)
	}
	return pair.Value, nil
}

// GetValues queries Consul for keys
func (c *ConsClient) GetValues(keys []string) (map[string][]byte, error) {
	vars := make(map[string][]byte)
	for _, key := range keys {
		pairs, _, err := c.client.List(key, nil)
		if err != nil {
			return vars, err
		}
		for _, p := range pairs {
			if p == nil {
				continue
			}
			vars[p.Key] = p.Value
		}
	}
	return vars, nil
}

func (c *ConsClient) handlePathEventDetail(lastPath2Val map[string][]byte, lastPath2Hash map[string]string, conPaths []string) (*Event, error) {
	result, err := c.GetValues(conPaths)
	if err != nil {
		logging.Errorf("GetValues failed:%s", err.Error())
		return nil, err
	}
	consulEvent := NewConsulEvent()
	for key, val := range result {
		var ceItem EventItem
		// check the value of Root, if not change, skip it
		hash, exist := lastPath2Hash[key]
		if exist && hash == utils.HashIt(val) {
			continue
		}
		lastPath2Hash[key] = utils.HashIt(val)
		logging.Debugf("get event from %s", key)
		// found add event
		_, exist = lastPath2Val[key]
		if !exist {
			logging.Infof("added event from %s", key)
			ceItem.EventType = config.EventAdded
		} else {
			logging.Infof("modified event from %s", key)
			ceItem.EventType = config.EventModified
		}
		// update last Root to value map
		lastPath2Val[key] = val
		ceItem.DataPath = key
		ceItem.DataValue = val
		consulEvent.Detail = append(consulEvent.Detail, ceItem)
	}
	// found delete event: exist in lastDataIDSet not in current data set
	needDeleteKeys := make([]string, 0)
	for key, val := range lastPath2Val {
		_, exist := result[key]
		if !exist {
			// found delete data ID
			logging.Infof("deleted event from %s", key)
			var ceItem EventItem
			ceItem.EventType = config.EventDeleted
			ceItem.DataPath = key
			ceItem.DataValue = val
			consulEvent.Detail = append(consulEvent.Detail, ceItem)
			needDeleteKeys = append(needDeleteKeys, key)
		}
	}
	// update the delete key
	for _, dk := range needDeleteKeys {
		delete(lastPath2Val, dk)
		delete(lastPath2Hash, dk)
	}

	return consulEvent, nil
}

// MonitorPath :
func (c *ConsClient) MonitorPath(conPaths []string) (<-chan *Event, error) {
	// check the Root valid or not
	_, err := c.GetValues(conPaths)
	if err != nil {
		logging.Errorf("can not monitor, GetValues failed:%s", err.Error())
		return nil, err
	}

	evCh := make(chan *Event, c.eventBufferSize)

	go func() {
		lastPath2Val := make(map[string][]byte)
		lastPath2Hash := make(map[string]string)
		conf := config.FromContext(c.ctx)
		tm := time.NewTicker(conf.GetDuration(ConfKeyCheckInterval))
		logging.Infof("monitoring consul Root %v", conPaths)
		for {
			select {
			case <-c.ctx.Done():
				tm.Stop()
				close(evCh)
				logging.Infof("receive done signal,will abandon monitor Root:%s", conPaths)
				return
			case <-tm.C:
				events, err := c.handlePathEventDetail(lastPath2Val, lastPath2Hash, conPaths)
				if err != nil {
					logging.Errorf("handlePathEventDetail failed: %v", err)
					continue
				}
				if len(events.Detail) > 0 {
					logging.Infof("consul monitor Root get %d events", len(events.Detail))
					evCh <- events
				}
			}
		}
	}()

	return evCh, nil
}
