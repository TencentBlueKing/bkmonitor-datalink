// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/spf13/cast"
	"github.com/spf13/viper"
	"golang.org/x/exp/slices"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	// FilePath path of logger
	FilePath = "./bmw.yaml"
	// EnvKeyPrefix env prefix
	EnvKeyPrefix = "bmw"

	// LoggerEnabledStdout enabled logger stdout
	LoggerEnabledStdout bool
	// LoggerLevel level of logger
	LoggerLevel string
	// Path paths of log
	Path string
	// MaxSize max size of split log file
	MaxSize int
	// MaxAge max age of split log file
	MaxAge int
	// MaxBackups max backup of log file
	MaxBackups int

	// BrokerRedisMode redis mode
	BrokerRedisMode string
	// BrokerRedisSentinelMasterName broker redis mater name
	BrokerRedisSentinelMasterName string
	// BrokerRedisSentinelAddress redis address
	BrokerRedisSentinelAddress []string
	// BrokerRedisSentinelPassword password of broker redis
	BrokerRedisSentinelPassword string
	// BrokerRedisStandaloneHost host of standalone broker redis
	BrokerRedisStandaloneHost string
	// BrokerRedisStandalonePort port of standalone broker redis
	BrokerRedisStandalonePort int
	// BrokerRedisStandalonePassword password of standalone broker redis
	BrokerRedisStandalonePassword string
	// BrokerRedisDatabase db of broker redis
	BrokerRedisDatabase int
	// BrokerRedisDialTimeout broker redis dial timeout
	BrokerRedisDialTimeout time.Duration
	// BrokerRedisReadTimeout broker redis dial timeout
	BrokerRedisReadTimeout time.Duration

	// StorageRedisMode mode of storage redis
	StorageRedisMode string
	// StorageRedisSentinelMasterName master name of storage redis
	StorageRedisSentinelMasterName string
	// StorageRedisSentinelAddress address of storage redis
	StorageRedisSentinelAddress []string
	// StorageRedisSentinelPassword password of storage redis
	StorageRedisSentinelPassword string
	// StorageRedisStandaloneHost host of storage redis
	StorageRedisStandaloneHost string
	// StorageRedisStandalonePort port of storage redis
	StorageRedisStandalonePort int
	// StorageRedisStandalonePassword password of storage redis
	StorageRedisStandalonePassword string
	// StorageRedisDatabase db of storage redis
	StorageRedisDatabase int
	// StorageRedisDialTimeout storage redis dial timeout
	StorageRedisDialTimeout time.Duration
	// StorageRedisReadTimeout storage redis read timeout
	StorageRedisReadTimeout time.Duration
	// StorageRedisKeyPrefix storage prefix
	StorageRedisKeyPrefix string

	// StorageDependentRedisMode dependent redis mode
	StorageDependentRedisMode string
	// StorageDependentRedisSentinelMasterName dependent redis master name
	StorageDependentRedisSentinelMasterName string
	// StorageDependentRedisSentinelAddress dependent redis address
	StorageDependentRedisSentinelAddress []string
	//StorageDependentRedisSentinelPassword dependent redis password
	StorageDependentRedisSentinelPassword string
	// StorageDependentRedisStandaloneHost dependent redis host
	StorageDependentRedisStandaloneHost string
	// StorageDependentRedisStandalonePort dependent redis(standalone) port
	StorageDependentRedisStandalonePort int
	// StorageDependentRedisStandalonePassword dependent redis(standalone) password
	StorageDependentRedisStandalonePassword string
	// StorageDependentRedisDatabase dependent redis db
	StorageDependentRedisDatabase int
	// StorageDependentRedisDialTimeout dependent redis dial timeout
	StorageDependentRedisDialTimeout time.Duration
	// StorageDependentRedisReadTimeout dependent redis read timeout
	StorageDependentRedisReadTimeout time.Duration

	// StorageConsulPathPrefix prefix of consul
	StorageConsulPathPrefix string
	// StorageConsulSrvName consul server name
	StorageConsulSrvName string
	// StorageConsulAddress consul address
	StorageConsulAddress string
	// StorageConsulPort consul port
	StorageConsulPort int
	// StorageConsulAddr consul address
	StorageConsulAddr string
	// StorageConsulTag tag of consul
	StorageConsulTag []string
	// StorageConsulTll consul ttl
	StorageConsulTll string

	// StorageMysqlHost mysql host
	StorageMysqlHost string
	// StorageMysqlPort mysql port
	StorageMysqlPort int
	// StorageMysqlUser mysql user
	StorageMysqlUser string
	// StorageMysqlPassword mysql password
	StorageMysqlPassword string
	// StorageMysqlDbName mysql db
	StorageMysqlDbName string
	// StorageMysqlCharset mysql charset
	StorageMysqlCharset string
	// StorageMysqlMaxIdleConnections mysql max idle
	StorageMysqlMaxIdleConnections int
	// StorageMysqlMaxOpenConnections mysql max open size
	StorageMysqlMaxOpenConnections int
	// StorageMysqlDebug enabled mysql debug
	StorageMysqlDebug bool

	// StorageEsUpdateTaskRetainInvalidAlias whether retain invalid alias
	StorageEsUpdateTaskRetainInvalidAlias bool

	// StorageBboltDefaultPath bbolt default path
	StorageBboltDefaultPath string
	// StorageBboltDefaultBucketName bbolt default bucket name
	StorageBboltDefaultBucketName string
	// StorageBboltDefaultSync bbolt default sync
	StorageBboltDefaultSync bool

	// WorkerQueues worker listen queue(only valid in worker process)
	WorkerQueues []string
	// WorkerConcurrency concurrency of worker task
	WorkerConcurrency int
	// WorkerHealthCheckInterval interval of worker report health status
	WorkerHealthCheckInterval time.Duration
	// WorkerHealthCheckInfoDuration cache duration of worker info
	WorkerHealthCheckInfoDuration time.Duration
	// WorkerDaemonTaskMaintainerInterval check interval of task maintainer
	WorkerDaemonTaskMaintainerInterval time.Duration
	// WorkerDaemonTaskRetryTolerateCount max retry of task
	WorkerDaemonTaskRetryTolerateCount int

	// SchedulerTaskWatchChanSize Listen for the maximum number of concurrent tasks in the broker queue
	SchedulerTaskWatchChanSize int
	// SchedulerDaemonTaskNumeratorInterval interval of scheduler numerator
	SchedulerDaemonTaskNumeratorInterval time.Duration
	// SchedulerDaemonTaskWorkerWatcherInterval interval of scheduler worker watcher
	SchedulerDaemonTaskWorkerWatcherInterval time.Duration
	// SchedulerDaemonTaskTaskWatcherInterval interval of scheduler task watcher
	SchedulerDaemonTaskTaskWatcherInterval time.Duration

	// GinMode http mode
	GinMode string
	// TaskListenHost http listen host
	TaskListenHost string
	// TaskListenPort http listen port
	TaskListenPort int
	// ControllerListenHost http listen host
	ControllerListenHost string
	// ControllerListenPort http listen port
	ControllerListenPort int
	// WorkerListenHost http listen host
	WorkerListenHost string
	// WorkerListenPort http listen port
	WorkerListenPort int

	// AesKey project aes key
	AesKey string
	// BkdataTokenSalt bkdata token salt
	BkdataTokenSalt string
	// BkdataAESIv bkdata AES IV
	BkdataAESIv string
	// BkdataAESKey bkdata AES Key
	BkdataAESKey string

	// enable multi-tenant mode
	EnableMultiTenantMode bool
	// BkApiEnabled enabled bk-apigw
	BkApiEnabled bool
	// BkApiUrl bk-apigw host
	BkApiUrl string
	// BkApiStage bk-apigw stage
	BkApiStage string
	// BkApiAppCode bk-apigw app code
	BkApiAppCode string
	// BkApiAppSecret bk-apigw app secret
	BkApiAppSecret string
	// BkApiBkdataApiBaseUrl bk-apigw bkdata base url
	BkApiBkdataApiBaseUrl string
	// BkApiGseApiGwUrl bk-apigw bkgse base url
	BkApiGseApiGwUrl string
	// BkApiCmdbApiGatewayUrl bk-apigw cmdb base url
	BkApiCmdbApiGatewayUrl string
	// BkMonitorApiGatewayBaseUrl 监控的apiGateway
	BkMonitorApiGatewayBaseUrl string
	// BkMonitorApiGatewayStage 监控的apiGateway的环境
	BkMonitorApiGatewayStage string

	// GoroutineLimit max size of task goroutine
	GoroutineLimit map[string]string

	// ESClusterMetricReportUrl es metric report config
	ESClusterMetricReportUrl         string
	ESClusterMetricReportDataId      int
	ESClusterMetricReportAccessToken string
	ESClusterMetricReportBlackList   []int

	// BigResourceTaskQueueName 占用大资源的队列名称
	BigResourceTaskQueueName string
)

func initVariables() {
	// LoggerEnabledStdout 是否开启日志文件输出
	LoggerEnabledStdout = GetValue("log.enableStdout", true)
	// LoggerLevel 日志等级
	LoggerLevel = GetValue("log.level", "info")
	// Path 日志文件输出路径
	Path = GetValue("log.path", "./bmw.log")
	// MaxSize 日志文件最大分裂大小
	MaxSize = GetValue("log.maxSize", 200)
	// MaxAge 日志文件最大存活时间
	MaxAge = GetValue("log.maxAge", 1)
	// MaxBackups 日志文件保存最大数量
	MaxBackups = GetValue("log.maxBackups", 5)

	/* Broker Redis 配置 */
	BrokerRedisMode = GetValue("broker.redis.mode", "standalone")
	BrokerRedisSentinelMasterName = GetValue("broker.redis.sentinel.masterName", "")
	BrokerRedisSentinelAddress = GetValue("broker.redis.sentinel.address", []string{"127.0.0.1"})
	BrokerRedisSentinelPassword = GetValue("broker.redis.sentinel.password", "")
	BrokerRedisStandaloneHost = GetValue("broker.redis.standalone.host", "127.0.0.1")
	BrokerRedisStandalonePort = GetValue("broker.redis.standalone.port", 6379)
	BrokerRedisStandalonePassword = GetValue("broker.redis.standalone.password", "")
	BrokerRedisDatabase = GetValue("broker.redis.db", 0)
	BrokerRedisDialTimeout = GetValue("broker.redis.dialTimeout", 10*time.Second, viper.GetDuration)
	BrokerRedisReadTimeout = GetValue("broker.redis.readTimeout", 10*time.Second, viper.GetDuration)

	/* Storage Redis 配置 */
	StorageRedisMode = GetValue("store.redis.mode", "standalone")
	StorageRedisSentinelMasterName = GetValue("store.redis.sentinel.masterName", "")
	StorageRedisSentinelAddress = GetValue("store.redis.sentinel.address", []string{"127.0.0.1"})
	StorageRedisSentinelPassword = GetValue("store.redis.sentinel.password", "")
	StorageRedisStandaloneHost = GetValue("store.redis.standalone.host", "127.0.0.1")
	StorageRedisStandalonePort = GetValue("store.redis.standalone.port", 6379)
	StorageRedisStandalonePassword = GetValue("store.redis.standalone.password", "")
	StorageRedisDatabase = GetValue("store.redis.db", 0)
	StorageRedisDialTimeout = GetValue("store.redis.dialTimeout", 10*time.Second, viper.GetDuration)
	StorageRedisReadTimeout = GetValue("store.redis.readTimeout", 10*time.Second, viper.GetDuration)
	StorageRedisKeyPrefix = GetValue("store.redis.keyPrefix", "bmw")

	/* Storage DependentRedis 配置 */
	StorageDependentRedisMode = GetValue("store.dependentRedis.mode", "standalone")
	StorageDependentRedisSentinelMasterName = GetValue("store.dependentRedis.sentinel.masterName", "")
	StorageDependentRedisSentinelAddress = GetValue("store.dependentRedis.sentinel.address", []string{"127.0.0.1"})
	StorageDependentRedisSentinelPassword = GetValue("store.dependentRedis.sentinel.password", "")
	StorageDependentRedisStandaloneHost = GetValue("store.dependentRedis.standalone.host", "127.0.0.1")
	StorageDependentRedisStandalonePort = GetValue("store.dependentRedis.standalone.port", 6379)
	StorageDependentRedisStandalonePassword = GetValue("store.dependentRedis.standalone.password", "")
	StorageDependentRedisDatabase = GetValue("store.dependentRedis.db", 0)
	StorageDependentRedisDialTimeout = GetValue("store.dependentRedis.dialTimeout", 10*time.Second, viper.GetDuration)
	StorageDependentRedisReadTimeout = GetValue("store.dependentRedis.readTimeout", 10*time.Second, viper.GetDuration)

	/* Storage Consul配置 */
	StorageConsulPathPrefix = GetValue("store.consul.pathPrefix", "bk_bkmonitorv3_enterprise_production")
	StorageConsulSrvName = GetValue("store.consul.srvName", "bmw")
	StorageConsulAddress = GetValue("store.consul.address", "127.0.0.1:8500")
	StorageConsulPort = GetValue("store.consul.port", 8500)
	StorageConsulAddr = GetValue("store.consul.addr", "http://127.0.0.1:8500")
	StorageConsulTag = GetValue("store.consul.tag", []string{"bmw"})
	StorageConsulTll = GetValue("store.consul.ttl", "")

	/* Storage Mysql配置 */
	StorageMysqlHost = GetValue("store.mysql.host", "127.0.0.1")
	StorageMysqlPort = GetValue("store.mysql.port", 3306)
	StorageMysqlUser = GetValue("store.mysql.user", "root")
	StorageMysqlPassword = GetValue("store.mysql.password", "")
	StorageMysqlDbName = GetValue("store.mysql.dbName", "")
	StorageMysqlCharset = GetValue("store.mysql.charset", "utf8")
	StorageMysqlMaxIdleConnections = GetValue("store.mysql.maxIdleConnections", 10)
	StorageMysqlMaxOpenConnections = GetValue("store.mysql.maxOpenConnections", 120)
	StorageMysqlDebug = GetValue("store.mysql.debug", false)

	StorageEsUpdateTaskRetainInvalidAlias = GetValue("store.es.esRetainInvalidAlias", false)

	StorageBboltDefaultPath = GetValue("store.bbolt.defaultPath", "bolt.db")
	StorageBboltDefaultBucketName = GetValue("store.bbolt.defaultBuckName", "spaceBucket")
	StorageBboltDefaultSync = GetValue("store.bbolt.defaultSync", false)

	/*
		Worker配置 ----- START
	*/
	// WorkerQueues worker进行监听的队列名称列表 在worker启动时可以通过--queues="x1,x2"指定 不指定默认使用default队列
	WorkerQueues = GetValue("worker.queues", []string{"default"})
	// WorkerConcurrency worker并发数量 0为使用CPU核数
	WorkerConcurrency = GetValue("worker.concurrency", 0)
	// WorkerHealthCheckInterval worker心跳上报时间间隔
	WorkerHealthCheckInterval = GetValue("worker.healthCheck.interval", 3*time.Second, viper.GetDuration)
	// WorkerHealthCheckInfoDuration worker心跳上报缓存过期时间
	WorkerHealthCheckInfoDuration = GetValue("worker.healthCheck.duration", 5*time.Second, viper.GetDuration)
	// WorkerDaemonTaskMaintainerInterval worker常驻任务检测任务是否正常运行的间隔
	WorkerDaemonTaskMaintainerInterval = GetValue(
		"worker.daemonTask.maintainer.interval", 5*time.Second, viper.GetDuration,
	)
	// WorkerDaemonTaskRetryTolerateCount worker常驻任务配置，当任务重试超过指定数量仍然失败时，下次重试间隔就不断动态增长
	WorkerDaemonTaskRetryTolerateCount = GetValue("worker.daemonTask.maintainer.tolerateCount", 60)
	/*
		Worker配置 ----- END
	*/

	/*
		Scheduler常驻任务配置 ----- START
	*/
	// SchedulerTaskWatchChanSize 调度器监听定时任务最大并发数量
	SchedulerTaskWatchChanSize = GetValue("scheduler.watcher.chanSize", 10)
	// SchedulerDaemonTaskNumeratorInterval 定时检测当前常驻任务分派是否正确的时间间隔(默认每60秒检测一次)
	SchedulerDaemonTaskNumeratorInterval = GetValue(
		"scheduler.daemonTask.numerator.interval", 60*time.Second, viper.GetDuration,
	)
	// SchedulerDaemonTaskWorkerWatcherInterval 常驻任务功能监听worker队列变化的间隔
	SchedulerDaemonTaskWorkerWatcherInterval = GetValue(
		"scheduler.daemonTask.watcher.workerWatchInterval", 1*time.Second, viper.GetDuration,
	)
	// SchedulerDaemonTaskTaskWatcherInterval 常驻任务功能监听task队列变化的间隔
	SchedulerDaemonTaskTaskWatcherInterval = GetValue(
		"scheduler.daemonTask.watcher.taskWatchInterval", 1*time.Second, viper.GetDuration,
	)
	/*
		Scheduler常驻任务配置 ----- END
	*/

	GinMode = GetValue("service.mode", "release")
	TaskListenHost = GetValue("service.task.listen", "127.0.0.1")
	TaskListenPort = GetValue("service.task.port", 10211)
	ControllerListenHost = GetValue("service.controller.listen", "127.0.0.1")
	ControllerListenPort = GetValue("service.controller.port", 10212)
	WorkerListenHost = GetValue("service.worker.listen", "127.0.0.1")
	WorkerListenPort = GetValue("service.worker.port", 10213)

	AesKey = GetValue("aes.key", "")
	BkdataTokenSalt = GetValue("aes.bkdataToken", "bk")
	BkdataAESIv = GetValue("aes.bkdataAESIv", "bkbkbkbkbkbkbkbk")
	BkdataAESKey = GetValue("aes.bkdataAESKey", "")

	EnableMultiTenantMode = GetValue("taskConfig.common.enableMultiTenantMode", false)
	BkApiEnabled = GetValue("taskConfig.common.bkapi.enabled", false)
	BkApiUrl = GetValue("taskConfig.common.bkapi.host", "http://127.0.0.1")
	BkApiStage = GetValue("taskConfig.common.bkapi.stage", "stag")
	BkApiAppCode = GetValue("taskConfig.common.bkapi.appCode", "appCode")
	BkApiAppSecret = GetValue("taskConfig.common.bkapi.appSecret", "appSecret")
	BkApiBkdataApiBaseUrl = GetValue("taskConfig.common.bkapi.bkdataApiBaseUrl", "")
	BkApiGseApiGwUrl = GetValue("taskConfig.common.bkapi.bkgseApiGwUrl", "")
	BkApiCmdbApiGatewayUrl = GetValue("taskConfig.common.bkapi.cmdbApiGatewayUrl", "")

	// BkMonitorApiGatewayBaseUrl 监控的apiGateway
	BkMonitorApiGatewayBaseUrl = GetValue("taskConfig.common.bkapi.bkmonitorApiGatewayBaseUrl", "")
	// BkMonitorApiGatewayStage 监控的apiGateway的环境
	BkMonitorApiGatewayStage = GetValue("taskConfig.common.bkapi.bkmonitorApiGatewayStage", "prod")

	GoroutineLimit = GetValue("taskConfig.common.goroutineLimit", map[string]string{}, viper.GetStringMapString)

	ESClusterMetricReportUrl = GetValue("taskConfig.logSearch.metric.reportUrl", "")
	ESClusterMetricReportDataId = GetValue("taskConfig.logSearch.metric.reportDataId", 100013)
	ESClusterMetricReportAccessToken = GetValue("taskConfig.logSearch.metric.reportAccessToken", "")
	ESClusterMetricReportBlackList = GetValue("taskConfig.logSearch.metric.reportBlackList", []int{}, viper.GetIntSlice)

	BigResourceTaskQueueName = GetValue("taskConfig.common.queues.bigResource", "big-resource")
}

var (
	keys []string
)

// GetValue get value from config file
func GetValue[T any](key string, def T, getter ...func(string) T) T {
	if !slices.Contains(keys, strings.ToLower(key)) && reflect.TypeOf(def).Kind() != reflect.Map {
		return def
	}

	if len(getter) != 0 {
		return getter[0](key)
	}

	value := viper.Get(key)
	if value == nil {
		logger.Warnf("Null configuration item(%s) was found! Check whether it is correct", key)
		return def
	}

	if reflect.TypeOf(value).Kind() == reflect.Slice {
		valueSlice := reflect.ValueOf(value)

		// Create a new slice with the same type as the default value
		resultSlice := reflect.MakeSlice(reflect.TypeOf(def), valueSlice.Len(), valueSlice.Len())

		// Iterate through the slice and set the values
		for i := 0; i < valueSlice.Len(); i++ {
			elem := valueSlice.Index(i).Interface()

			// Check if the element type matches the default slice element type
			if reflect.TypeOf(elem).AssignableTo(reflect.TypeOf(def).Elem()) {
				resultSlice.Index(i).Set(reflect.ValueOf(elem))
			} else {
				panic(fmt.Sprintf("element of type %T is not assignable to type %T", elem, reflect.TypeOf(def).Elem()))
			}
		}

		return resultSlice.Interface().(T)
	}

	return value.(T)
}

func GetFloatSlice(key string) []float64 {
	items, err := cast.ToSliceE(viper.Get(key))
	if err != nil {
		panic(fmt.Sprintf("failed to get float slice of key: %s, error: %s", key, err))
	}
	var res []float64
	for index, item := range items {
		switch item.(type) {
		case float64:
			res = append(res, item.(float64))
		case int:
			res = append(res, float64(item.(int)))
		default:
			panic(fmt.Sprintf("config: %s[%d] type not supported", key, index))
		}
	}

	return res
}

// InitConfig This method is used to refresh the configuration
// and should only be called once in the project.
// The purpose of this method is not private is that it can be called in the test file.
func InitConfig() {
	viper.SetConfigFile(FilePath)

	if err := viper.ReadInConfig(); err != nil {
		pwd, _ := os.Getwd()
		logger.Fatalf("read config file: %s in %s error: %s", FilePath, pwd, err)
	}
	viper.AutomaticEnv()
	viper.SetEnvPrefix(EnvKeyPrefix)
	replacer := strings.NewReplacer(".", "_")
	viper.SetEnvKeyReplacer(replacer)
	keys = viper.AllKeys()

	initVariables()
	initMetadataVariables()
	initClusterMetricVariables()
	initApmVariables()
	initAlarmConfig()
}
