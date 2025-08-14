// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package beater

import (
	"context"
	"fmt"
	nethttp "net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/beater/schedulerfactory"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/beater/taskfactory"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define/stats"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/http"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/reloader"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/host"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	CMDBLevelKey = "bk_cmdb_level"
)

// beaterState beater运行状态
type beaterState struct {
	config          *configs.Config
	name            string
	version         string
	ctx             context.Context
	cancelFunc      context.CancelFunc
	heartBeatTicker *time.Ticker

	Scheduler        define.Scheduler
	KeywordScheduler define.Scheduler
	ListenScheduler  define.Scheduler
}

func newBeaterState() *beaterState {
	return &beaterState{
		config: configs.NewConfig(),
	}
}

// beaterStatus 运行数据
type beaterStatus struct {
	startAt     time.Time
	reloadCount int32
	reloadAt    time.Time

	successCount int32
	failCount    int32
	errorCount   int32

	loadedTasks int32
}

// IncrErrorCount 出错计数
func (s *beaterStatus) IncrErrorCount() int32 {
	return atomic.AddInt32(&s.errorCount, 1)
}

// IncrSuccessCount 成功计数
func (s *beaterStatus) IncrSuccessCount() int32 {
	return atomic.AddInt32(&s.successCount, 1)
}

// IncrFailCount 失败计数
func (s *beaterStatus) IncrFailCount() int32 {
	return atomic.AddInt32(&s.failCount, 1)
}

// updateByMapStr 按返回状态码计数
func (s *beaterStatus) updateByMapStr(status int32) {
	logger.Debugf("get event status: %v", status)
	if status == define.GatherStatusOK {
		s.IncrSuccessCount()
	} else if status == define.GatherStatusUnknown {
		s.IncrErrorCount()
	} else {
		s.IncrFailCount()
	}
}

func newBeaterStatus() *beaterStatus {
	now := time.Now()
	return &beaterStatus{
		startAt:     now,
		reloadCount: 0,
		reloadAt:    now,
	}
}

// MonitorBeater 采集器主程序对象
type MonitorBeater struct {
	*beaterState
	*beaterStatus

	hostIDWatcher host.Watcher

	configEngine define.ConfigEngine
	status       define.Status
	EventChan    chan define.Event

	HookPreRun     utils.HookManager
	HookPostRun    utils.HookManager
	HookPreReload  utils.HookManager
	HookPostReload utils.HookManager

	sig                 chan os.Signal
	executable          string
	tempDir             string
	adminServer         *nethttp.Server
	adminServerReloader *reloader.Reloader
	rawConfig           *common.Config
}

// New : new a beater
func New(cfg *common.Config, name, version string) (*MonitorBeater, error) {
	ctx, cancel := context.WithCancel(context.Background())
	state := newBeaterState()
	state.ctx = ctx
	state.cancelFunc = cancel
	state.name = name
	state.version = version

	bt := MonitorBeater{
		beaterStatus: newBeaterStatus(),
		beaterState:  state,
		sig:          make(chan os.Signal, 1),
	}

	signal.Notify(bt.sig, syscall.SIGINT, syscall.SIGTERM)
	err := bt.ParseConfig(cfg)
	if err != nil {
		return nil, err
	}
	bt.rawConfig = cfg
	configs.SetContainerMode(beat.IsContainerMode())
	state.heartBeatTicker = time.NewTicker(bt.config.HeartBeat.Period)

	registerGseMarshalFunc(bt.config.JsonLib)

	exe, err := os.Executable()
	if err == nil {
		bt.executable = exe
	}
	// 非调试模式时初始化临时目录
	if !bt.config.KeepOneDimension {
		bt.tempDir, err = utils.InitTempDir(true)
		if err != nil {
			return nil, fmt.Errorf("InitTempDir with error: %w", err)
		}
	}

	return &bt, nil
}

// initHostIDWatcher 监听cmdb下发host id文件
func (bt *MonitorBeater) initHostIDWatcher() error {
	var err error
	if bt.hostIDWatcher != nil {
		err = bt.hostIDWatcher.Reload(bt.ctx, bt.config.HostIDPath, bt.config.CmdbLevelMaxLength, bt.config.MustHostIDExist)
		if err != nil {
			logger.Warnf("reload watch host id failed,error:%s", err.Error())
			// 不影响其他位置的reload
			return nil
		}
		return nil
	}

	// 将watcher初始化并启动
	hostConfig := host.Config{
		HostIDPath:         bt.config.HostIDPath,
		CMDBLevelMaxLength: bt.config.CmdbLevelMaxLength,
		IgnoreCmdbLevel:    bt.config.IgnoreCmdbLevel,
		MustHostIDExist:    bt.config.MustHostIDExist,
	}
	bt.hostIDWatcher = host.NewWatcher(bt.ctx, hostConfig)
	err = bt.hostIDWatcher.Start()
	if err != nil {
		logger.Warnf("start watch host id failed,filepath:%s,cmdb max length:%d,error:%s", bt.config.HostIDPath, bt.config.CmdbLevelMaxLength, err)
		return err
	}
	define.GlobalWatcher = bt.hostIDWatcher
	gse.RegisterHostWatcher(bt.hostIDWatcher)

	return nil
}

// ParseConfig 读取配置
func (bt *MonitorBeater) ParseConfig(cfg *common.Config) error {
	var err error
	ctx := bt.ctx
	bt.configEngine = NewBaseConfigEngine(ctx)

	err = bt.configEngine.Init(cfg, bt)
	if err != nil {
		return fmt.Errorf("init configEngine failed: %v", err)
	}

	err = bt.configEngine.CleanTaskConfigList()
	if err != nil {
		return fmt.Errorf("clean taskConfigList failed: %v", err)
	}

	globalConfig, ok := bt.configEngine.GetGlobalConfig().(*configs.Config)
	if !ok {
		return fmt.Errorf("get globalConfig failed")
	}
	configs.DisableNetlink = globalConfig.DisableNetLink
	bt.config = globalConfig
	err = bt.initHostIDWatcher()
	if err != nil {
		return fmt.Errorf("init hostid failed,error:%s", err)
	}

	// 使用hostid文件中的ip和云区域cloudId信息，替换掉全局配置中的ip和云区域cloudId
	if bt.hostIDWatcher != nil {
		cloudId := bt.hostIDWatcher.GetCloudId()
		value, e := strconv.Atoi(cloudId)
		if e == nil {
			bt.config.CloudID = int32(value)
		}
		bt.config.IP = bt.hostIDWatcher.GetHostInnerIp()
		// 发送event时用 云区域id:主机内网IP 替换node_id
		bt.config.NodeID = cloudId + ":" + bt.config.IP
		bizId := bt.hostIDWatcher.GetBizId()
		if bizId != 0 {
			bt.config.BizID = int32(bizId)
		}
	}

	return nil
}

// GetTasks 生成任务对象
func (bt *MonitorBeater) GetTasks() []define.Task {
	confTypeList := bt.configEngine.GetTaskConfigList()

	tasks := make([]define.Task, len(confTypeList))

	for i, confType := range confTypeList {
		tasks[i] = taskfactory.New(bt.config, confType)
	}

	return tasks
}

// PreRun : before run
func (bt *MonitorBeater) PreRun() error {
	bt.EventChan = make(chan define.Event, bt.config.EventBufferSize)
	logger.Infof("event buffer size config:%d", bt.config.EventBufferSize)

	bt.Scheduler = schedulerfactory.New(bt, bt.config, bt.config.Mode)
	bt.KeywordScheduler = schedulerfactory.New(bt, bt.config, schedulerfactory.SchedulerTypeKeyword)
	bt.ListenScheduler = schedulerfactory.New(bt, bt.config, schedulerfactory.SchedulerTypeListen)

	tasks := bt.GetTasks()
	updateRunningTasks(tasks)
	bt.loadedTasks += int32(len(tasks))

	for _, task := range tasks {
		if task.GetConfig() == nil {
			continue
		}
		t := task.GetConfig().GetType()
		switch t {
		case configs.ConfigTypeKeyword:
			bt.KeywordScheduler.Add(task)
		case configs.ConfigTypeTrap, configs.ConfigTypeMetric, configs.ConfigTypeKubeevent, configs.ConfigTypeDmesg:
			bt.ListenScheduler.Add(task)
		default:
			bt.Scheduler.Add(task)
		}
	}
	bt.HookPreRun.Apply(bt.ctx)

	return nil
}

func (bt *MonitorBeater) waitScheduler() error {
	timer := time.NewTimer(bt.config.CleanUpTimeout)
loop:
	for {
		if bt.Scheduler.GetStatus() == define.SchedulerFinished &&
			bt.ListenScheduler.GetStatus() == define.SchedulerFinished &&
			bt.KeywordScheduler.GetStatus() == define.SchedulerFinished {
			break
		}
		select {
		case <-timer.C:
			logger.Warn("wait scheduler clean up timeout")
			bt.cancelFunc()
			break loop
		case event := <-bt.EventChan:
			logger.Debugf("wait scheduler publish event: %v", event)
			bt.PublishEvent(event)
		}
	}
	timer.Stop()

	return nil
}

func (bt *MonitorBeater) waitChannel() error {
	logger.Info("cleaning up event channel")
	close(bt.EventChan)
	for event := range bt.EventChan {
		bt.PublishEvent(event)
	}
	return nil
}

// PostRun : post run
func (bt *MonitorBeater) PostRun() error {
	beginAt := time.Now()
	logger.Infof("bkmonitorbeat cleaning up at: %v", beginAt)

	bt.heartBeatTicker.Stop()

	var err error
	err = bt.waitScheduler()
	if err != nil {
		return err
	}

	logger.Info("waiting scheduler")
	bt.Scheduler.Wait()
	bt.KeywordScheduler.Wait()
	bt.ListenScheduler.Wait()

	logger.Info("closing event channel")
	err = bt.waitChannel()
	if err != nil {
		return err
	}
	status := bt.Scheduler.GetStatus()
	if status != define.SchedulerFinished {
		logger.Warnf("scheduler not finished: %v", status)
	}
	status = bt.KeywordScheduler.GetStatus()
	if status != define.SchedulerFinished {
		logger.Warnf("keyword scheduler not finished: %v", status)
	}
	status = bt.ListenScheduler.GetStatus()
	if status != define.SchedulerFinished {
		logger.Warnf("listen scheduler not finished: %v", status)
	}

	beat.Stop()

	bt.HookPostRun.Apply(bt.ctx)
	return nil
}

// PublishHeartBeat : publish heartbeat event
func (bt *MonitorBeater) PublishHeartBeat(now time.Time) {
	state := bt.beaterState
	if state.Scheduler.IsDaemon() {
		// 如果是bkMonitorbeat，需要使用新式的方法发送
		if bt.IsMonitorBeat() {
			err := bt.configEngine.RefreshHeartBeat()
			if err != nil {
				logger.Errorf("RefreshHeartBeat failed,error:%v", err)
				return
			}
			// 判断如果是bkmonitorbeat，需要发送全局及子任务的心跳
			err = bt.configEngine.SendHeartBeat()
			if err != nil {
				logger.Errorf("SendHeartBeat failed,error:%v", err)
				return
			}
		} else {
			// 否则使用uptimecheckbeat的方式发送心跳
			event := NewHeartBeatEvent(bt)
			beat.Send(event.AsMapStr())
		}
	}
}

// PublishEvent : publish event
func (bt *MonitorBeater) PublishEvent(event define.Event) {
	// 异常捕捉
	defer utils.RecoverFor(func(err error) {
		logger.Errorf("publish event panic: %v", err)
		bt.IncrErrorCount()
	})

	// 空event是进行流程中止用的，不需要publish
	if event == nil {
		logger.Debug("get empty event")
		return
	}
	beatEvent := NewBeatEvent(bt, event)
	mapStr := beatEvent.AsMapStr()
	// 如果配置了忽略，则不增加cmdb_level
	if !bt.config.IgnoreCmdbLevel && !event.IgnoreCMDBLevel() {
		// 如果GetInfo没有错误，则增加cmdb_level
		if info, err := bt.hostIDWatcher.GetInfo(); err != nil {
			logger.Warnf("get error while try to get cmdb_level from watcher,error:%s", err)
		} else {
			mapStr[CMDBLevelKey] = info
		}
	}

	// 获取status参数，判断任务成功还是失败,进行统计计数
	var status int32
	i, ok := mapStr["status"]
	if ok {
		status = i.(int32)
		bt.beaterStatus.updateByMapStr(status)
	}
	// 非拨测任务,status统计结束后，屏蔽status上报
	// 拨测任务则会上报status，这是拨测的原有上报格式,不做改动
	if _, ok = mapStr["not_uptimecheck"]; ok {
		delete(mapStr, "status")
		delete(mapStr, "not_uptimecheck")
		// 非拨测任务中，具有错误的任务event无法正确返回结果，所以不进行publish
		if status != define.GatherStatusOK {
			return
		}
	}

	beat.Send(mapStr)
}

// writePidStore 写入pid文件
func (bt *MonitorBeater) writePidStore() {
	tick := time.Tick(time.Minute)
	for {
		select {
		case <-tick:
			content := define.GlobalPidStore.Bytes()
			if len(content) <= 0 {
				continue
			}
			p := filepath.Join(path.Dir(bt.executable), utils.PidStoreFile())
			if err := os.WriteFile(p, content, 0x666); err != nil {
				logger.Errorf("failed to write fakeproc file, err: %v", err)
			}
		case <-bt.ctx.Done():
			return
		}
	}
}

// Run : start and run beaterl
func (bt *MonitorBeater) Run() error {
	defer utils.RecoverFor(func(err error) {
		logger.Errorf("beater run panic: %v", err)
		bt.IncrErrorCount()
	})
	// 清理临时目录
	if bt.tempDir != "" {
		defer func() {
			err := os.RemoveAll(bt.tempDir)
			if err != nil {
				logger.Errorf("remove temp dir failed: %v", err)
			}
		}()
	}
	logger.Info("MonitorBeater is running! Hit CTRL-C to stop it")

	var err error
	err = bt.PreRun()
	if err != nil {
		return err
	}
	bt.startAdminServer()
	bt.startAdminServerReloader()

	err = bt.Scheduler.Start(bt.ctx)
	if err != nil {
		return err
	}

	err = bt.KeywordScheduler.Start(bt.ctx)
	if err != nil {
		return err
	}

	err = bt.ListenScheduler.Start(bt.ctx)
	if err != nil {
		return err
	}
	go bt.writePidStore()

	bt.status = define.BeaterStatusRunning
	if bt.config.HeartBeat.PublishImmediately {
		bt.PublishHeartBeat(time.Now())
	}

loop:
	for bt.status == define.BeaterStatusRunning {
		select {
		case <-bt.sig:
			logger.Info("Hit Control+C, process exit")
			break loop
		case <-beat.ReloadChan:
			cfg := beat.GetConfig()
			bt.Reload(cfg)
		case <-bt.ctx.Done():
			logger.Info("context done")
			break loop
		case event := <-bt.EventChan:
			bt.PublishEvent(event)
			logger.Debugf("beater publish event: %v", event)
		case now, ok := <-bt.heartBeatTicker.C:
			if ok {
				bt.PublishHeartBeat(now)
			}
		}
	}
	logger.Infof("beater break loop with status: %v", bt.status)

	bt.Scheduler.Stop()
	bt.KeywordScheduler.Stop()
	bt.ListenScheduler.Stop()
	err = bt.PostRun()
	if err != nil {
		return err
	}
	bt.status = define.BeaterStatusTerminated

	logger.Info("MonitorBeater exit")
	return nil
}

// Stop : stop beater
func (bt *MonitorBeater) Stop() {
	if bt.status != define.BeaterStatusRunning {
		return
	}
	bt.hostIDWatcher.Stop()
	bt.status = define.BeaterStatusTerminating
	bt.EventChan <- nil
	err := bt.stopAdminServer()
	if err != nil {
		logger.Errorf("stop admin server failed: %v", err)
	}
	bt.stopAdminServerReloader()
	logger.Info("shutting down")
}

func updateRunningTasks(tasks []define.Task) {
	count := make(map[string]int)
	for i := 0; i < len(tasks); i++ {
		task := tasks[i]
		if task.GetConfig() != nil {
			count[task.GetConfig().GetType()]++
		}
	}
	stats.SetRunningTasks(count)
}

// Reload : reload conf
func (bt *MonitorBeater) Reload(cfg *common.Config) {
	logger.Info("MonitorBeater reload")
	stats.IncReload()

	oldState := bt.beaterState
	oldConfig := bt.config
	oldConfigEngine := bt.configEngine
	state := newBeaterState()
	state.ctx = oldState.ctx
	state.cancelFunc = oldState.cancelFunc
	state.heartBeatTicker = oldState.heartBeatTicker
	state.Scheduler = oldState.Scheduler
	state.KeywordScheduler = oldState.KeywordScheduler
	state.ListenScheduler = oldState.ListenScheduler

	bt.beaterState = state
	err := bt.ParseConfig(cfg)
	if err != nil {
		logger.Errorf("MonitorBeater reload error: %v", err)
		bt.beaterState = oldState
		bt.config = oldConfig
		bt.configEngine = oldConfigEngine
		return
	}

	tasks := bt.GetTasks()
	updateRunningTasks(tasks)
	bt.loadedTasks += int32(len(tasks))
	beatTasks := make([]define.Task, 0)
	keywordTasks := make([]define.Task, 0)
	listenTasks := make([]define.Task, 0)
	for _, task := range tasks {
		if task.GetConfig() == nil {
			continue
		}
		t := task.GetConfig().GetType()
		switch t {
		case configs.ConfigTypeKeyword:
			keywordTasks = append(keywordTasks, task)
		case configs.ConfigTypeTrap, configs.ConfigTypeMetric, configs.ConfigTypeKubeevent, configs.ConfigTypeDmesg:
			listenTasks = append(listenTasks, task)
		default:
			beatTasks = append(beatTasks, task)
		}
	}

	err = bt.Scheduler.Reload(bt.ctx, state.config, beatTasks)
	if err != nil {
		logger.Errorf("Scheduler reload error: %v", err)
		bt.beaterState = oldState
		bt.config = oldConfig
		bt.configEngine = oldConfigEngine
		return
	}
	err = bt.KeywordScheduler.Reload(bt.ctx, state.config, keywordTasks)
	if err != nil {
		logger.Errorf("keywordScheduler reload error: %v", err)
		bt.beaterState = oldState
		bt.config = oldConfig
		bt.configEngine = oldConfigEngine
	}
	err = bt.ListenScheduler.Reload(bt.ctx, state.config, listenTasks)
	if err != nil {
		logger.Errorf("listenScheduler reload error: %v", err)
		bt.beaterState = oldState
		bt.config = oldConfig
		bt.configEngine = oldConfigEngine
	}
	metricsReloaded := false
	if bt.config.BizID != oldConfig.BizID || bt.config.CloudID != oldConfig.CloudID || bt.config.IP != oldConfig.IP {
		// host数据变化时指标重置
		metricsReloaded = true
	}
	if metricsReloaded || bt.config.AdminAddr != oldConfig.AdminAddr {
		err = bt.restartAdminServer()
		if err != nil {
			logger.Errorf("restart admin server failed: %v", err)
		}
	}
	bt.reloadCount++
	bt.reloadAt = time.Now()
	logger.Infof("config:%+v", bt.config)
}

// GetEventChan :
func (bt *MonitorBeater) GetEventChan() chan define.Event {
	return bt.EventChan
}

// GetConfig :
func (bt *MonitorBeater) GetConfig() define.Config {
	return bt.config
}

// GetScheduler :
func (bt *MonitorBeater) GetScheduler() define.Scheduler {
	return bt.Scheduler
}

// GetKeywordScheduler :
func (bt *MonitorBeater) GetKeywordScheduler() define.Scheduler {
	return bt.KeywordScheduler
}

// GetListenScheduler :
func (bt *MonitorBeater) GetListenScheduler() define.Scheduler {
	return bt.ListenScheduler
}

func (bt *MonitorBeater) IsMonitorBeat() bool {
	return true
}

type reloaderFunc func(*common.Config)

func (f reloaderFunc) Reload(c *common.Config) {
	f(c)
}

// startAdminServerReloader 调试服务重载
func (bt *MonitorBeater) startAdminServerReloader() {
	bt.adminServerReloader = reloader.NewReloader(bt.name, reloaderFunc(func(cfg *common.Config) {
		logger.Info("reload admin server")
		newConfig := configs.NewConfig()
		err := cfg.Unpack(newConfig)
		if err != nil {
			logger.Errorf("admin server reload unpack cfg failed: %v", err)
			return
		}
		if newConfig.AdminAddr != bt.config.AdminAddr {
			bt.config.AdminAddr = newConfig.AdminAddr
			logger.Info("restart admin server on ", bt.config.AdminAddr)
			err = bt.restartAdminServer()
			if err != nil {
				logger.Errorf("admin server reload restart failed: %v", err)
				return
			}
		}
	}), reloader.WithReloaderSig2())
	if err := bt.adminServerReloader.Run(""); err != nil {
		logger.Errorf("start admin server reloader failed,error: %v", err)
	}
}

func (bt *MonitorBeater) stopAdminServerReloader() {
	bt.adminServerReloader.Stop()
}

// startAdminServer 启用调试服务
func (bt *MonitorBeater) startAdminServer() {
	if bt.config.AdminAddr == "" {
		return
	}
	bt.adminServer = http.NewServer(bt.config.AdminAddr)

	go func() {
		err := bt.adminServer.ListenAndServe()
		if err != nil {
			logger.Errorf("start admin server failed,error: %v", err)
		}
	}()
}

func (bt *MonitorBeater) stopAdminServer() error {
	if bt.adminServer == nil {
		return nil
	}
	return bt.adminServer.Shutdown(context.Background())
}

func (bt *MonitorBeater) restartAdminServer() error {
	err := bt.stopAdminServer()
	if err != nil {
		return err
	}
	bt.startAdminServer()
	return nil
}
