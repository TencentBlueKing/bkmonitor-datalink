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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/spf13/viper"
	"golang.org/x/exp/slices"
	"reflect"
	"strings"
)

var (
	FilePath     = "./bmw.yaml"
	EnvKeyPrefix = "bmw"

	TaskWatchChanSize int

	LoggerEnabledStdout        bool
	LoggerLevel                string
	LoggerStdoutPath           string
	LoggerStdoutFileMaxSize    int
	LoggerStdoutFileMaxAge     int
	LoggerStdoutFileMaxBackups int

	BrokerRedisMode               string
	BrokerRedisSentinelMasterName string
	BrokerRedisSentinelAddress    []string
	BrokerRedisSentinelPassword   string
	BrokerRedisStandaloneHost     string
	BrokerRedisStandalonePort     int
	BrokerRedisStandalonePassword string
	BrokerRedisDatabase           int
	BrokerRedisDialTimeout        int
	BrokerRedisReadTimeout        int

	StorageRedisMode               string
	StorageRedisSentinelMasterName string
	StorageRedisSentinelAddress    []string
	StorageRedisSentinelPassword   string
	StorageRedisStandaloneHost     string
	StorageRedisStandalonePort     int
	StorageRedisStandalonePassword string
	StorageRedisDatabase           int
	StorageRedisDialTimeout        int
	StorageRedisReadTimeout        int
	StorageRedisKeyPrefix          string

	StorageDependentRedisMode               string
	StorageDependentRedisSentinelMasterName string
	StorageDependentRedisSentinelAddress    []string
	StorageDependentRedisSentinelPassword   string
	StorageDependentRedisStandaloneHost     string
	StorageDependentRedisStandalonePort     int
	StorageDependentRedisStandalonePassword string
	StorageDependentRedisDatabase           int
	StorageDependentRedisDialTimeout        int
	StorageDependentRedisReadTimeout        int

	StorageConsulPathPrefix string
	StorageConsulSrvName    string
	StorageConsulAddress    string
	StorageConsulPort       int
	StorageConsulAddr       string
	StorageConsulTag        []string
	StorageConsulTll        string

	StorageMysqlHost               string
	StorageMysqlPort               int
	StorageMysqlUser               string
	StorageMysqlPassword           string
	StorageMysqlDbName             string
	StorageMysqlCharset            string
	StorageMysqlMaxIdleConnections int
	StorageMysqlMaxOpenConnections int
	StorageMysqlDebug              bool

	WorkerQueues                          []string
	WorkerConcurrency                     int
	WorkerHealthCheckInterval             int
	WorkerHealthCheckInfoDuration         int
	WorkerDaemonTaskMaintainerInterval    int
	WorkerDaemonTaskRetryTolerateCount    int
	WorkerDaemonTaskRetryTolerateInterval int
	WorkerDaemonTaskRetryIntolerantFactor int

	SchedulerDaemonTaskNumeratorInterval     int
	SchedulerDaemonTaskWorkerWatcherInterval int
	SchedulerDaemonTaskTaskWatcherInterval   int

	HttpGinMode      string
	HttpListenPath   string
	HttpListenPort   int
	HttpEnabledPprof bool

	AesKey string

	TestStorageMysqlHost     string
	TestStorageMysqlPort     int
	TestStorageMysqlUser     string
	TestStorageMysqlPassword string
	TestStorageMysqlDbName   string
)

func initVariables() {

	// TaskWatchChanSize 调度器监听定时任务最大并发数量
	TaskWatchChanSize = GetValue("task.watcher.chanSize", 10)

	// LoggerEnabledStdout 是否开启日志文件输出
	LoggerEnabledStdout = GetValue("log.enableStdout", true)
	// LoggerLevel 日志等级
	LoggerLevel = GetValue("log.level", "info")
	// LoggerStdoutPath 日志文件输出路径
	LoggerStdoutPath = GetValue("log.stdoutPath", "./bmw.log")
	// LoggerStdoutFileMaxSize 日志文件最大分裂大小
	LoggerStdoutFileMaxSize = GetValue("log.stdoutFileMaxSize", 200)
	// LoggerStdoutFileMaxAge 日志文件最大存活时间
	LoggerStdoutFileMaxAge = GetValue("log.stdoutFileMaxAge", 1)
	// LoggerStdoutFileMaxBackups 日志文件保存最大数量
	LoggerStdoutFileMaxBackups = GetValue("log.stdoutFileMaxBackups", 5)

	/* Broker Redis 配置 */
	BrokerRedisMode = GetValue("broker.redis.mode", "standalone")
	BrokerRedisSentinelMasterName = GetValue("broker.redis.sentinel.masterName", "")
	BrokerRedisSentinelAddress = GetValue("broker.redis.sentinel.address", []string{"127.0.0.1"})
	BrokerRedisSentinelPassword = GetValue("broker.redis.sentinel.password", "")
	BrokerRedisStandaloneHost = GetValue("broker.redis.standalone.host", "127.0.0.1")
	BrokerRedisStandalonePort = GetValue("broker.redis.standalone.port", 6379)
	BrokerRedisStandalonePassword = GetValue("broker.redis.standalone.password", "")
	BrokerRedisDatabase = GetValue("broker.redis.db", 0)
	BrokerRedisDialTimeout = GetValue("broker.redis.dialTimeout", 10)
	BrokerRedisReadTimeout = GetValue("broker.redis.readTimeout", 10)

	/* Storage Redis 配置 */
	StorageRedisMode = GetValue("store.redis.mode", "standalone")
	StorageRedisSentinelMasterName = GetValue("store.redis.sentinel.masterName", "")
	StorageRedisSentinelAddress = GetValue("store.redis.sentinel.address", []string{"127.0.0.1"})
	StorageRedisSentinelPassword = GetValue("store.redis.sentinel.password", "")
	StorageRedisStandaloneHost = GetValue("store.redis.standalone.host", "127.0.0.1")
	StorageRedisStandalonePort = GetValue("store.redis.standalone.port", 6379)
	StorageRedisStandalonePassword = GetValue("store.redis.standalone.password", "")
	StorageRedisDatabase = GetValue("store.redis.db", 0)
	StorageRedisDialTimeout = GetValue("store.redis.dialTimeout", 10)
	StorageRedisReadTimeout = GetValue("store.redis.readTimeout", 10)
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
	StorageDependentRedisDialTimeout = GetValue("store.dependentRedis.dialTimeout", 10)
	StorageDependentRedisReadTimeout = GetValue("store.dependentRedis.readTimeout", 10)

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
	StorageMysqlMaxOpenConnections = GetValue("store.mysql.maxOpenConnections", 100)
	StorageMysqlDebug = GetValue("store.mysql.debug", false)

	/*
		Worker配置 ----- START
	*/
	// WorkerQueues worker进行监听的队列名称列表 在worker启动时可以通过--queues="x1,x2"指定 不指定默认使用default队列
	WorkerQueues = GetValue("worker.queues", []string{"default"})
	// WorkerConcurrency worker并发数量 0为使用CPU核数
	WorkerConcurrency = GetValue("worker.concurrency", 0)
	// WorkerHealthCheckInterval worker心跳上报时间间隔 单位: s
	WorkerHealthCheckInterval = GetValue("worker.healthCheck.interval", 3)
	// WorkerHealthCheckInfoDuration worker心跳上报缓存过期时间 单位: s
	WorkerHealthCheckInfoDuration = GetValue("worker.healthCheck.duration", 5)
	// WorkerDaemonTaskMaintainerInterval worker常驻任务检测任务是否正常运行的间隔 单位: s
	WorkerDaemonTaskMaintainerInterval = GetValue("worker.daemonTask.maintainer.interval", 1)
	// WorkerDaemonTaskRetryTolerateCount worker常驻任务配置，当任务重试超过指定数量仍然失败时，下次重试就不断动态增长
	WorkerDaemonTaskRetryTolerateCount = GetValue("worker.daemonTask.maintainer.tolerateCount", 60)
	// WorkerDaemonTaskRetryTolerateInterval worker常驻任务当任务执行失败并且重试次数未超过 WorkerDaemonTaskRetryTolerateCount 时
	// 下次重试时间间隔
	WorkerDaemonTaskRetryTolerateInterval = GetValue("worker.daemonTask.maintainer.tolerateInterval", 10)
	// WorkerDaemonTaskRetryIntolerantFactor worker常驻任务当任务重试次数超过 WorkerDaemonTaskRetryTolerateCount 时
	// 下次重试按照Nx倍数增长 设置倍数因子
	WorkerDaemonTaskRetryIntolerantFactor = GetValue("worker.daemonTask.maintainer.intolerantFactor", 2)
	/*
		Worker配置 ----- END
	*/

	/*
		Scheduler常驻任务配置 ----- START
	*/
	// SchedulerDaemonTaskNumeratorInterval 定时检测当前常驻任务分派是否正确的时间间隔(默认每60秒检测一次)
	SchedulerDaemonTaskNumeratorInterval = GetValue("scheduler.daemonTask.numerator.interval", 60)
	// SchedulerDaemonTaskWorkerWatcherInterval 常驻任务功能监听worker队列变化的间隔 单位: s
	SchedulerDaemonTaskWorkerWatcherInterval = GetValue("scheduler.daemonTask.watcher.workerWatchInterval", 1)
	// SchedulerDaemonTaskTaskWatcherInterval 常驻任务功能监听task队列变化的间隔 单位: s
	SchedulerDaemonTaskTaskWatcherInterval = GetValue("scheduler.daemonTask.watcher.taskWatchInterval", 1)
	/*
		Scheduler常驻任务配置 ----- END
	*/

	HttpGinMode = GetValue("service.http.mode", "release")
	HttpListenPath = GetValue("service.http.listen", "127.0.0.1")
	HttpListenPort = GetValue("service.http.port", 10213)
	HttpEnabledPprof = GetValue("service.http.enablePprof", true)

	AesKey = GetValue("aes.key", "")

	TestStorageMysqlHost = GetValue("test.store.mysql.host", "127.0.0.1")
	TestStorageMysqlPort = GetValue("test.store.mysql.port", 3306)
	TestStorageMysqlUser = GetValue("test.store.mysql.user", "root")
	TestStorageMysqlPassword = GetValue("test.store.mysql.password", "")
	TestStorageMysqlDbName = GetValue("test.store.mysql.dbName", "")
}

var (
	keys []string
)

func GetValue[T any](key string, def T) T {
	if !slices.Contains(keys, strings.ToLower(key)) {
		return def
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

// InitConfig This method is used to refresh the configuration
// and should only be called once in the project.
// The purpose of this method is not private is that it can be called in the test file.
func InitConfig() {
	viper.SetConfigFile(FilePath)

	if err := viper.ReadInConfig(); err != nil {
		logger.Errorf("Read config file:s error: %s", FilePath, err)
	}
	viper.AutomaticEnv()
	viper.SetEnvPrefix(EnvKeyPrefix)
	replacer := strings.NewReplacer(".", "_")
	viper.SetEnvKeyReplacer(replacer)
	keys = viper.AllKeys()

	initVariables()
	initMetadataVariables()
	initApmVariables()
}
