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
	"net/http"
	"os"
	"strings"

	"github.com/cstockton/go-conv"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// WrapConsulService :
func WrapConsulService(current define.Service, plugins ...consul.ServicePlugin) define.Service {
	for _, plugin := range plugins {
		logging.PanicIf(plugin.Wrap(current))
		current = plugin
	}
	return current
}

// ConsulPairToPipelineConfig :
func ConsulPairToPipelineConfig(_ define.WatchEventType, data interface{}) (interface{}, error) {
	pair, ok := data.(*consul.KVPair)
	if !ok {
		return nil, errors.Wrapf(define.ErrType, "%T not supported", data)
	} else if pair == nil {
		return nil, errors.Wrapf(define.ErrValue, "can not be nil")
	}
	var conf config.PipelineConfig
	err := json.Unmarshal(pair.Value, &conf)
	return &conf, err
}

// ConsulWatchScheduler :
type ConsulWatchScheduler struct {
	*Scheduler
	watcher define.ServiceWatcher
}

// NewConsulWatchScheduler :
func NewConsulWatchScheduler(ctx context.Context, name string) (*ConsulWatchScheduler, error) {
	builder := NewConsulWatchSchedulerBuilder(ctx, name)
	return builder.Build()
}

// DispatchConverter :
type DispatchConverter struct {
	source, target string
}

// ElementCreator :
func (c *DispatchConverter) ElementCreator(element *consul.KVPair) ([]define.IDer, error) {
	if element == nil {
		return nil, errors.Wrapf(define.ErrOperationForbidden, "pair is nil")
	}

	e := consul.DispatchItemConf{
		Pair: element,
	}
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
	return utils.NewDetailsBalanceElementsWithID(e.Config.DataID, &e, partition), nil
}

// NodeCreator :
func (c *DispatchConverter) NodeCreator(node *define.ServiceInfo) (define.IDer, error) {
	if node == nil {
		return nil, errors.Wrapf(define.ErrOperationForbidden, "service info is nil")
	}

	return utils.NewNodeWithID(node.ID, node), nil
}

// ShadowCreator :
func (c *DispatchConverter) ShadowCreator(node define.IDer, element define.IDer) (string, string, string, error) {
	v, ok := node.(*utils.DetailsBalanceElement)
	if !ok {
		return "", "", "", errors.Wrapf(define.ErrType, "node %T not supported", node)
	} else if node == nil {
		return "", "", "", errors.Wrapf(define.ErrType, "node is nil")
	}

	info, ok := v.Details.(*define.ServiceInfo)
	if !ok {
		return "", "", "", errors.Wrapf(define.ErrType, "node %T not supported", v.Details)
	} else if info == nil {
		return "", "", "", errors.Wrapf(define.ErrType, "node is nil")
	}

	v, ok = element.(*utils.DetailsBalanceElement)
	if !ok {
		return "", "", "", errors.Wrapf(define.ErrType, "element %T not supported", element)
	} else if element == nil {
		return "", "", "", errors.Wrapf(define.ErrType, "element is nil")
	}

	item, ok := v.Details.(*consul.DispatchItemConf)
	if !ok {
		return "", "", "", errors.Wrapf(define.ErrType, "element %T not supported", v.Details)
	} else if item == nil {
		return "", "", "", errors.Wrapf(define.ErrType, "element is nil")
	}

	if item.Pair == nil {
		return "", "", "", errors.Wrapf(define.ErrValue, "element pair is nil")
	}

	path := strings.TrimPrefix(item.Pair.Key, c.source)
	return item.Pair.Key, fmt.Sprintf("%s/%s%s", c.target, info.ID, path), info.ID, nil
}

// ShadowDetector
func (c *DispatchConverter) ShadowDetector(pair *consul.KVPair) (source, target, service string, err error) {
	path := strings.TrimPrefix(pair.Key, c.target)
	service, sep, path := utils.Partition(strings.TrimPrefix(path, "/"), "/")
	if sep == "" {
		return "", "", "", define.ErrOperationForbidden
	}
	return fmt.Sprintf("%s/%s", c.source, path), pair.Key, service, nil
}

// NewDispatchConverter :
func NewDispatchConverter(source, target string) *DispatchConverter {
	return &DispatchConverter{
		source: source,
		target: target,
	}
}

// ConsulWatchSchedulerBuilder :
type ConsulWatchSchedulerBuilder struct {
	Name          string
	Context       context.Context
	Configuration define.Configuration
	TaskManager   *define.TaskManager
	Client        consul.ClientAPI

	ServiceRoot        string
	DataIDRoot         string
	ManualRoot         string
	ShadowedDataIDRoot string
	ShadowedDataIDPath string

	ServiceConfig *consul.ServiceConfig
	Service       define.Service
	Session       define.Session

	DataIDDiffWatcher define.ServiceWatcher

	WatcherFunc Watcher

	PeriodTriggerCreator  consul.TriggerCreator
	ServiceTriggerCreator consul.TriggerCreator
	PairTriggerCreator    consul.TriggerCreator
	TriggerCreator        consul.TriggerCreator

	DispatchConverter consul.DispatchConverter
	Dispatcher        *consul.Dispatcher
}

// NewConsulWatchSchedulerBuilder :
func NewConsulWatchSchedulerBuilder(ctx context.Context, name string) *ConsulWatchSchedulerBuilder {
	conf := config.FromContext(ctx)
	return &ConsulWatchSchedulerBuilder{
		Name:          name,
		Context:       ctx,
		Configuration: conf,
	}
}

// BuildTaskManager
func (b *ConsulWatchSchedulerBuilder) BuildTaskManager() error {
	b.TaskManager = define.NewTaskManager()
	return nil
}

// BuildClient :
func (b *ConsulWatchSchedulerBuilder) BuildClient() error {
	client, err := consul.NewConsulAPIFromConfig(b.Configuration)
	if err != nil {
		return err
	}
	b.Client = client
	return nil
}

// BuildServiceRoot
func (b *ConsulWatchSchedulerBuilder) BuildServiceRoot() error {
	b.ServiceRoot = b.Configuration.GetString(consul.ConfKeyServicePath)
	return nil
}

// BuildServiceConfig :
func (b *ConsulWatchSchedulerBuilder) BuildServiceConfig() error {
	conf := b.Configuration
	address := conf.GetString(define.ConfHost)
	port := conf.GetInt(define.ConfPort)

	name := conf.GetString(consul.ConfKeyServiceName)
	id := fmt.Sprintf("%s-%s", name, define.ServiceID)

	tag := conf.GetString(consul.ConfKeyServiceTag)

	clusterID := conf.GetString(consul.ConfKeyClusterID)
	// 供寻找某个集群实例时使用的tag
	clusterTag := strings.Join([]string{clusterID, tag, "service"}, "-")
	b.ServiceConfig = &consul.ServiceConfig{
		ID:              id,
		Name:            name,
		Address:         address,
		Port:            port,
		TTL:             conf.GetDuration(consul.ConfKeyClientTTL),
		SessionBehavior: consul.SessionBehaviorDelete,
		Namespace:       b.ServiceRoot,
		Tags:            []string{tag + "-service", tag, clusterTag},
		Meta: map[string]string{
			"version":    define.Version,
			"pid":        conv.String(os.Getpid()),
			"cluster_id": clusterID,
			"service":    name,
			"module":     tag,
		},
		ClusterTag: clusterTag,
	}
	return nil
}

// BuildService :
func (b *ConsulWatchSchedulerBuilder) BuildService() error {
	service := consul.NewService(b.Context, b.Client, b.ServiceConfig)
	b.Service = WrapConsulService(
		service,
		consul.NewElectionPlugin(),
		consul.NewTTLCheckPlugin(),
		consul.NewMetaInfoPlugin(),
	)
	b.TaskManager.Add(b.Service)
	return nil
}

// BuildSession
func (b *ConsulWatchSchedulerBuilder) BuildSession() error {
	b.Session = b.Service.Session()
	return nil
}

// BuildDataIDPath :
func (b *ConsulWatchSchedulerBuilder) BuildDataIDPath() error {
	conf := b.Configuration
	b.DataIDRoot = conf.GetString(consul.ConfKeyDataIDPath)
	b.ManualRoot = conf.GetString(consul.ConfKeyManualPath)
	b.ShadowedDataIDRoot = utils.ResolveUnixPath(b.ServiceRoot, "data_id")
	b.ShadowedDataIDPath = utils.ResolveUnixPaths(b.ShadowedDataIDRoot, b.ServiceConfig.ID)
	return nil
}

// BuildDataIDDiffWatcher :
func (b *ConsulWatchSchedulerBuilder) BuildDataIDDiffWatcher() error {
	path := b.ShadowedDataIDPath
	watcher, err := consul.NewShadowPrefixDiffWatcher(&consul.WatcherConfig{
		Client:    b.Client,
		Context:   b.Context,
		Converter: ConsulPairToPipelineConfig,
	}, path, false)
	if err != nil {
		return err
	}
	b.DataIDDiffWatcher = watcher
	b.WatcherFunc = func(ctx context.Context) <-chan *define.WatchEvent {
		logging.Infof("watch pipeline config from %s", path)
		return watcher.Events()
	}
	b.TaskManager.Add(watcher)

	return nil
}

// BuildServiceTriggerCreator :
func (b *ConsulWatchSchedulerBuilder) BuildServiceTriggerCreator() error {
	var tag string
	if len(b.ServiceConfig.Tags) > 0 {
		// 多集群下需要使用含有cluster信息的tag查询
		tag = b.ServiceConfig.ClusterTag
	}
	logging.Debugf("service trigger by tag %s", tag)

	b.ServiceTriggerCreator = consul.NewServiceTriggerCreator(b.Client, b.DataIDRoot, b.ServiceConfig.Name, tag)
	return nil
}

// BuildPairTriggerCreator :
func (b *ConsulWatchSchedulerBuilder) BuildPairTriggerCreator() error {
	b.PairTriggerCreator = consul.NewPairTriggerCreator(b.Client, b.DataIDRoot, b.Service)
	return nil
}

// BuildPeriodTriggerCreator
func (b *ConsulWatchSchedulerBuilder) BuildPeriodTriggerCreator() error {
	period := b.Configuration.GetDuration(consul.ConfKeyDispatchInterval)
	b.PeriodTriggerCreator = consul.NewPeriodTriggerCreator(b.Client, b.DataIDRoot, b.Service, period)
	return nil
}

// BuildTriggerCreator :
func (b *ConsulWatchSchedulerBuilder) BuildTriggerCreator() error {
	serviceTriggerCreator := b.ServiceTriggerCreator
	pairTriggerCreator := b.PairTriggerCreator
	PeriodTriggerCreator := b.PeriodTriggerCreator

	b.TriggerCreator = func(ctx context.Context, ch chan *consul.DispatchItem) define.Task {
		manager := define.NewTaskManager()
		manager.Add(serviceTriggerCreator(ctx, ch))
		manager.Add(pairTriggerCreator(ctx, ch))
		manager.Add(PeriodTriggerCreator(ctx, ch))
		return manager
	}

	return nil
}

// BuildDispatchConverter :
func (b *ConsulWatchSchedulerBuilder) BuildDispatchConverter() error {
	b.DispatchConverter = NewDispatchConverter(b.DataIDRoot, b.ShadowedDataIDRoot)
	return nil
}

// BuildDispatcher :
func (b *ConsulWatchSchedulerBuilder) BuildDispatcher() error {
	// dispatcher 用于给各transfer实例分配dataid
	b.Dispatcher = consul.NewDispatcher(consul.DispatcherConfig{
		Context:         b.Context,
		Converter:       b.DispatchConverter,
		Client:          b.Client,
		TargetRoot:      b.ShadowedDataIDRoot,
		ManualRoot:      b.ManualRoot,
		TriggerCreator:  b.TriggerCreator,
		DispatchDelay:   b.Configuration.GetDuration(consul.ConfKeyDispatchDelay),
		RecoverInterval: b.Configuration.GetDuration(consul.ConfKeyDispatchInterval),
	})
	return b.Dispatcher.Wrap(b.Service)
}

// Build :
func (b *ConsulWatchSchedulerBuilder) Build() (*ConsulWatchScheduler, error) {
	funcs := []func() error{
		// 任务管理器，用于启动一些除了流水线业务之外的后台任务
		b.BuildTaskManager,
		// 创建consul的client
		b.BuildClient,
		// 将transfer抽象成一个服务，“service就是shadow”
		b.BuildServiceRoot,
		// 拉取transfer服务配置信息
		b.BuildServiceConfig,
		// 创建transfer服务，并注册到consul
		b.BuildService,
		// 创建一个调度器中的同步锁，leader会拿它到consul中
		b.BuildSession,
		// 创建DataIDPath，创建shadow，DataID跟踪表示
		b.BuildDataIDPath,
		// 创建DispatchConverter
		b.BuildDispatchConverter,
		// 监听DataIDPath和shadow
		b.BuildDataIDDiffWatcher,
		// 触发器，监听一些成员变量等
		b.BuildServiceTriggerCreator,
		// 监听某些前缀
		b.BuildPairTriggerCreator,
		// 周期拉取一些k，v，service
		b.BuildPeriodTriggerCreator,
		// 创建以上触发器
		b.BuildTriggerCreator,
		// leader专享，构建整个调度器
		b.BuildDispatcher,
	}

	for _, fn := range funcs {
		err := fn()
		if err != nil {
			return nil, err
		}
	}

	scheduler, err := NewSchedulerWithTaskManager(b.Context, b.Name, b.WatcherFunc, b.TaskManager, b.Service)
	if err != nil {
		return nil, err
	}

	return &ConsulWatchScheduler{
		Scheduler: scheduler,
		watcher:   b.DataIDDiffWatcher,
	}, nil
}

// ClusterHelper
type ClusterHelper struct {
	*ConsulWatchSchedulerBuilder
}

func (c *ClusterHelper) serviceInfoToMappings(infos []*define.ServiceInfo) map[string]*define.ServiceInfo {
	mappings := make(map[string]*define.ServiceInfo, len(infos))
	for _, i := range infos {
		mappings[i.ID] = i
	}
	return mappings
}

// ListServices
func (c *ClusterHelper) ListServices() (map[string]*define.ServiceInfo, error) {
	infos, err := c.Service.Info(define.ServiceTypeAll)
	if err != nil {
		return nil, err
	}

	return c.serviceInfoToMappings(infos), nil
}

// ListAllServices  列出所有集群的所有transfer实例
func (c *ClusterHelper) ListAllServices() (map[string]*define.ServiceInfo, error) {
	infos, err := c.Service.Info(define.ServiceTypeClusterAll)
	if err != nil {
		return nil, err
	}

	return c.serviceInfoToMappings(infos), nil
}

// ListAllLeaders  列出所有集群中的leader
func (c *ClusterHelper) ListAllLeaders() (map[string]*define.ServiceInfo, error) {
	infos, err := c.Service.Info(define.ServiceTypeLeaderAll)
	if err != nil {
		return nil, err
	}

	return c.serviceInfoToMappings(infos), nil
}

// ListLeaders
func (c *ClusterHelper) ListLeaders() (map[string]*define.ServiceInfo, error) {
	infos, err := c.Service.Info(define.ServiceTypeLeader)
	if err != nil {
		return nil, err
	}

	return c.serviceInfoToMappings(infos), nil
}

// NewClusterHelper
func NewClusterHelper(ctx context.Context, conf define.Configuration) (*ClusterHelper, error) {
	helper := ClusterHelper{
		ConsulWatchSchedulerBuilder: NewConsulWatchSchedulerBuilder(config.IntoContext(ctx, conf), ""),
	}
	_, err := helper.Build()
	if err != nil {
		return nil, err
	}
	return &helper, nil
}

// ServiceInfoView
func ServiceInfoView(_ define.ServiceType) func(http.ResponseWriter, *http.Request) {
	return func(writer http.ResponseWriter, request *http.Request) {
		helper, err := NewClusterHelper(context.Background(), config.Configuration)
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			_, err := writer.Write([]byte(err.Error()))
			logging.PanicIf(err)
		}

		service := helper.Service
		services, err := service.Info(define.ServiceTypeAll)
		logging.PanicIf(err)

		writer.WriteHeader(http.StatusOK)
		logging.PanicIf(json.NewEncoder(writer).Encode(services))
	}
}

func init() {
	define.RegisterScheduler("watch", func(ctx context.Context, name string) (define.Scheduler, error) {
		if config.FromContext(ctx) == nil {
			return nil, define.ErrOperationForbidden
		}
		return NewConsulWatchScheduler(ctx, name)
	})

	http.HandleFunc("/consul/services", ServiceInfoView(define.ServiceTypeAll))
	http.HandleFunc("/consul/leader", ServiceInfoView(define.ServiceTypeLeader))
}
