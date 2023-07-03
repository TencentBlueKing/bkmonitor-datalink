// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package broker

import (
	"fmt"
	"time"

	"github.com/gocelery/gocelery"
	"github.com/gomodule/redigo/redis"
	"github.com/spf13/viper"
)

const (
	redisHostPath        = "broker.redis.host"
	redisPortPath        = "broker.redis.port"
	redisPasswordPath    = "broker.redis.password"
	redisDatabasePath    = "broker.redis.database"
	redisMaxIdlePath     = "broker.redis.max_idle"
	redisMaxActivePath   = "broker.redis.max_active"
	redisIdleTimeoutPath = "broker.redis.idle_timeout"
	redisQueueNamePath   = "broker.redis.queue_name"
)

func setRedisDefault() {
	viper.SetDefault(redisPortPath, 6379)
	viper.SetDefault(redisDatabasePath, 0)
	viper.SetDefault(redisMaxIdlePath, 1)
	viper.SetDefault(redisMaxActivePath, 0)
	viper.SetDefault(redisIdleTimeoutPath, 0)
	viper.SetDefault(redisQueueNamePath, "celery")
}

func init() {
	setRedisDefault()
}

// TODO: 是否支持 cluster 和 sentinel
func getRedisPool() *redis.Pool {
	redisURL := fmt.Sprintf("redis://%s:%d", viper.GetString(redisHostPath), viper.GetInt(redisPortPath))
	redisPool := &redis.Pool{
		MaxIdle:     viper.GetInt(redisMaxIdlePath),
		MaxActive:   viper.GetInt(redisMaxActivePath),
		IdleTimeout: time.Duration(viper.GetInt(redisIdleTimeoutPath)) * time.Second,
		Dial: func() (redis.Conn, error) {
			c, err := redis.DialURL(redisURL)
			if err != nil {
				panic(fmt.Errorf("dial redis error, %s", err))
			}
			// 验证redis密码
			if _, authErr := c.Do("AUTH", viper.GetString(redisPasswordPath)); authErr != nil {
				c.Close()
				panic(fmt.Errorf("redis auth password error: %s", authErr))
			}
			// 验证选择的database
			if _, err := c.Do("SELECT", viper.GetInt(redisDatabasePath)); err != nil {
				c.Close()
				panic(fmt.Errorf("select redis database error: %s", err))
			}
			return c, nil
		},
		// 测试可以通过
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			if _, err := c.Do("PING"); err != nil {
				c.Close()
				panic(fmt.Errorf("ping redis error: %s", err))
			}
			return nil
		},
	}
	return redisPool
}

func newRedisBroker() *gocelery.RedisCeleryBroker {
	redisPool := getRedisPool()
	return gocelery.NewRedisBroker(redisPool, viper.GetString(redisQueueNamePath))
}

func newRedisBackend() *gocelery.RedisCeleryBackend {
	redisPool := getRedisPool()
	return gocelery.NewRedisBackend(redisPool)
}
