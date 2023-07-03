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
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// IndexItem
type IndexItem struct {
	Index uint64
	Data  interface{}
}

// IndexHelper
type IndexHelper struct {
	cache           bool
	remain, current map[string]*IndexItem
	doer            func(id string, evType define.WatchEventType, data interface{})
}

// Doer
func (h *IndexHelper) Doer(doer func(id string, evType define.WatchEventType, data interface{})) {
	h.doer = doer
}

// Update
func (h *IndexHelper) Update(id string, index uint64, data interface{}) bool {
	evType := define.WatchEventModified

	item := IndexItem{Index: index}
	if h.cache {
		item.Data = data
	}
	h.current[id] = &item

	last, ok := h.remain[id]
	if !ok {
		evType = define.WatchEventAdded
	} else {
		delete(h.remain, id)
		if last.Index == index {
			return false
		}
	}
	h.doer(id, evType, data)
	return true
}

// Rotate
func (h *IndexHelper) Rotate() {
	remain := h.remain
	current := make(map[string]*IndexItem, len(h.remain))
	h.remain = h.current
	h.current = current

	for id, item := range remain {
		h.doer(id, define.WatchEventDeleted, item.Data)
	}
}

// NewIndexHelper
func NewIndexHelper(cache bool) *IndexHelper {
	return &IndexHelper{
		cache:   cache,
		remain:  make(map[string]*IndexItem),
		current: make(map[string]*IndexItem),
	}
}

// ShadowCopierConfig
type ShadowCopierConfig struct {
	Watcher    define.ServiceWatcher
	Client     ClientAPI
	Context    context.Context
	Prefix     string
	Dispatcher func(ev *define.WatchEvent) []string
}

func shadowDelete(client ClientAPI, targets ...string) error {
	api := client.KV()
	err := utils.NewMultiErrors()
	for _, target := range targets {
		_, e := api.Delete(target, nil)
		err.Add(e)
	}
	return err.AsError()
}

// GetSourceByShadowedPair
func GetSourceByShadowedPair(shadowed *KVPair) (*KVPair, error) {
	var pair KVPair
	return &pair, json.Unmarshal(shadowed.Value, &pair)
}

// GetShadowBySourcePair
func GetShadowBySourcePair(target string, source *KVPair) (*KVPair, error) {
	value, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}

	return &KVPair{
		Key:   target,
		Value: value,
	}, nil
}

func shadowUpdate(client ClientAPI, pair *KVPair, targets ...string) error {
	shadowed, err := GetShadowBySourcePair("", pair)
	if err != nil {
		return err
	}

	api := client.KV()
	errs := utils.NewMultiErrors()
	for _, target := range targets {
		shadowed.Key = target
		_, e := api.Put(shadowed, nil)
		errs.Add(e)
	}
	return errs.AsError()
}

// ShadowSync
var ShadowSync = func(ctx context.Context, client ClientAPI, source string, target ...string) (err error) {
	var pair *KVPair = nil
	if source != "" {
		api := client.KV()
		logging.Debugf("get shadowed source %s to sync", source)
		pair, _, err = api.Get(source, NewQueryOptions(ctx))
		if err != nil {
			return errors.Wrapf(err, "get source key %s", source)
		}

	}

	if pair == nil {
		logging.Debugf("delete shadowed target %s", target)
		return shadowDelete(client, target...)
	}

	logging.Debugf("update shadowed target %s", target)
	return shadowUpdate(client, pair, target...)
}

// ShadowInfo
type ShadowInfo struct {
	Service string
}

// BatchShadowCopier
type BatchShadowCopier struct {
	*define.ContextTask
	define.Atomic
	*BatchShadowCopierConfig
	mappings map[string]map[string]*ShadowInfo
}

// String
func (c *BatchShadowCopier) String() string {
	buf := bytes.NewBuffer(nil)
	c.Each(func(source, target string, info *ShadowInfo) bool {
		_, err := fmt.Fprintf(buf, "%s --> %s\n", source, target)
		utils.CheckError(err)
		return true
	})

	return buf.String()
}

// Link
func (c *BatchShadowCopier) Link(source, target, service string) bool {
	created := false
	c.Update(func() {
		links, ok := c.mappings[source]
		if !ok {
			links = make(map[string]*ShadowInfo)
			c.mappings[source] = links
		}

		_, found := links[target]
		if !found {
			links[target] = &ShadowInfo{
				Service: service,
			}
			created = true
		}
	})

	return created
}

func (c *BatchShadowCopier) isLink(source, target string) bool {
	links, ok := c.mappings[source]
	if !ok {
		return false
	}

	_, found := links[target]
	return found
}

// IsLink
func (c *BatchShadowCopier) IsLink(source, target string) bool {
	isLink := false
	c.View(func() {
		isLink = c.isLink(source, target)
	})
	return isLink
}

// Unlink
func (c *BatchShadowCopier) Unlink(source, target string) bool {
	deleted := false
	c.Update(func() {
		links, ok := c.mappings[source]
		if !ok {
			return
		}

		_, found := links[target]
		if found {
			deleted = true
			delete(links, target)
			if len(links) == 0 {
				delete(c.mappings, source)
			}
		}
	})
	return deleted
}

// Clear
func (c *BatchShadowCopier) Clear() {
	c.Update(func() {
		c.mappings = make(map[string]map[string]*ShadowInfo)
	})
}

// Each
func (c *BatchShadowCopier) Each(fn func(source, target string, info *ShadowInfo) bool) {
	c.View(func() {
		for source, links := range c.mappings {
			for target, info := range links {
				if !fn(source, target, info) {
					return
				}
			}
		}
	})
}

// Sync
func (c *BatchShadowCopier) Sync(source string, target string) error {
	found := false
	c.View(func() {
		links, ok := c.mappings[source]
		if !ok {
			return
		}

		_, found = links[target]
	})

	shadowedSource := source
	if !found {
		shadowedSource = ""
	}

	logging.Infof("shadow update from %s to %s", source, target)
	return ShadowSync(c.Context, c.Client, shadowedSource, target)
}

// SyncAll
func (c *BatchShadowCopier) SyncAll() error {
	syncErr := utils.NewMultiErrors()

	logging.Debugf("shadow copier sync all")
	mappings := map[string][]string{}
	c.View(func() {
		for source, links := range c.mappings {
			targets := make([]string, 0, len(links))
			for target := range links {
				targets = append(targets, target)
			}
			mappings[source] = targets
		}
	})

	for source, targets := range mappings {
		logging.Debugf("shadow copier sync %s to %v", source, targets)
		syncErr.Add(ShadowSync(c.Context, c.Client, source, targets...))
	}
	return syncErr.AsError()
}

// BatchShadowCopierConfig
type BatchShadowCopierConfig struct {
	Context  context.Context
	Prefix   string
	Client   ClientAPI
	Interval time.Duration
}

// Init
func (c BatchShadowCopierConfig) Init() BatchShadowCopierConfig {
	if c.Interval == 0 {
		c.Interval = 3 * time.Second
	}
	return c
}

// NewBatchShadowCopierWithWatcher
func NewBatchShadowCopierWithWatcher(conf BatchShadowCopierConfig) *BatchShadowCopier {
	conf = conf.Init()
	copier := &BatchShadowCopier{
		BatchShadowCopierConfig: &conf,
		mappings:                make(map[string]map[string]*ShadowInfo),
	}
	copier.ContextTask = define.NewContextTask(conf.Context, func(ctx context.Context) {
		ticker := time.NewTicker(conf.Interval)
		defer ticker.Stop()

		logging.Infof("shadow copier ready")
	loop:
		for {
			select {
			case <-ctx.Done():
				break loop
			case <-ticker.C:
				logging.Debugf("shadow copier triggered")
				err := copier.SyncAll()
				if err != nil {
					logging.Errorf("shadow sync error %v", err)
				}
			}
		}
		logging.Infof("shadow copier stopped")
	})

	return copier
}

// NewBatchShadowCopier
func NewBatchShadowCopier(conf BatchShadowCopierConfig) (*BatchShadowCopier, error) {
	conf = conf.Init()
	return NewBatchShadowCopierWithWatcher(conf), nil
}

// ShadowRecover
func ShadowRecover(ctx context.Context, client ClientAPI, target string, copier ShadowCopier, converter DispatchConverter) error {
	api := client.KV()
	pairs, _, err := api.List(target, NewQueryOptions(ctx))
	if err != nil {
		return err
	}

	logging.Infof("found %d shadowed links", len(pairs))
	for _, pair := range pairs {
		shadowedSource, shadowedTarget, service, err := converter.ShadowDetector(pair)
		if err != nil {
			logging.Errorf("detect shadow link on %s err %v", pair.Key, err)
			continue
		}
		copier.Link(shadowedSource, shadowedTarget, service)
	}

	return nil
}
