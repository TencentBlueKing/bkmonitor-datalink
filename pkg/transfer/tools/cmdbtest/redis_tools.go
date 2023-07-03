// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
)

/*
	用于redisStore压测，批量刷主机类 cmdb 缓存到redis中。
*/

var (
	addr     = flag.String("addr", "127.0.0.1:6380", "redis addr")
	password = flag.String("password", "", "redis password")
	key      = flag.String("key", "001.bkmonitorv3.transfer.cmdb.cache", "hash key")
	db       = flag.Int("db", 0, "db index")
	count    = flag.Int("count", 500000, "hash len")
	partSize = flag.Int("partSize", 100, "part size")
)

func main() {
	flag.Parse()
	client := redis.NewClient(&redis.Options{
		Addr:     *addr,
		Password: *password,
		DB:       *db,
	})
	defer func() { _ = client.Close() }()

	// 其实这里的key是什么值都可以
	baseStr := `{"data":"eyJUb3BvIjpbeyJia19iaXpfaWQiOiIzIiwiYmtfbW9kdWxlX2lkIjoiMTM2IiwiYmtfc2V0X2lkIjoiMTEifV0sIkJpeklEIjpbM10sImlwIjoiMTAuMC4xLjU5IiwiYmtfY2xvdWRfaWQiOjB9","expires_at":"2021-08-17T00:29:01.607205179+08:00"}`

	item := new(StoreItem)

	err := json.Unmarshal([]byte(baseStr), &item)
	if err != nil {
		fmt.Println("err occurred : ", err)
	}
	fmt.Printf("item.Data: %s\n", string(item.Data))
	fmt.Printf("item.Expire: %s\n", item.ExpiresAt)

	var (
		n       int
		setList = make([]interface{}, 0, *partSize)
		ctx     = context.Background()
		startIP = 0x2000000
		firstM  = 0xFF000000
		secondM = 0x00FF0000
		thirdM  = 0x0000FF00
		fourthM = 0x000000FF
	)
	for i := 0; i < *count; i++ {
		startIP++
		first := startIP & firstM >> 24
		second := startIP & secondM >> 16
		third := startIP & thirdM >> 8
		fourth := startIP & fourthM
		k := fmt.Sprintf("model-host-0-%d.%d.%d.%d", first, second, third, fourth)

		fmt.Println(k)
		n++
		setList = append(setList, k, baseStr)
		if n < *partSize {
			continue
		}
		n = 0
		client.HSet(ctx, *key, setList...)
		setList = make([]interface{}, 0, *partSize)
	}
	if len(setList) > 0 {
		client.HSet(ctx, *key, setList...)
	}
	client.HSet(ctx, StoreFlag, "x")
}

const (
	StoreNoExpires time.Duration = 0
	StoreFlag                    = "bootstrap_update"
)

type StoreItem struct {
	Data      []byte     `json:"data"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func (s *StoreItem) SetExpires(t time.Duration) {
	if t != StoreNoExpires {
		now := time.Now()
		expiresAt := now.Add(t)
		s.ExpiresAt = &expiresAt
	} else {
		s.ExpiresAt = nil
	}
}
