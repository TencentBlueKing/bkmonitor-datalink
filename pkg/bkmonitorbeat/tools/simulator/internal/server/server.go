// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package server

import (
	"fmt"
	"io"
	"log"
	"sync"
	"time"
)

const (
	serviceNameHTTP    = "http"
	serviceNameTCP     = "tcp"
	serviceNameUDP     = "udp"
	serviceNameICMP    = "icmp"
	serviceNameProcess = "process"
	serviceNameProm    = "prom"
)

var AllServiceNames = []string{
	serviceNameHTTP,
	serviceNameTCP,
	serviceNameUDP,
	serviceNameICMP,
	serviceNameProcess,
	serviceNameProm,
}

type TestServerConfig struct {
	HTTPPort       int
	HTTPResponse   string
	TCPPort        int
	TCPResponse    string
	UDPPort        int
	UDPResponse    string
	ProcessTCPPort int
	ProcessUDPPort int
	PromPort       int
}

type service struct {
	name  string
	serve func()
}

var normal bool

var normalMutex sync.RWMutex

func isNormal() bool {
	normalMutex.RLock()
	defer normalMutex.RUnlock()
	return normal
}

func handleResponse(w io.Writer, response string) {
	var body string
	if isNormal() {
		body = response
	} else {
		body = fmt.Sprintln("error response", time.Now())
	}
	_, _ = io.WriteString(w, body)
}

func getServices(c *TestServerConfig) []service {
	return []service{
		{
			name: serviceNameHTTP,
			serve: func() {
				err := serveHTTP(c.HTTPPort, c.HTTPResponse)
				if err != nil {
					log.Fatalln(err)
				}
			},
		},
		{
			name: serviceNameTCP,
			serve: func() {
				err := serveTCP(c.TCPPort, c.TCPResponse)
				if err != nil {
					log.Fatalln(err)
				}
			},
		},
		{
			name: serviceNameUDP,
			serve: func() {
				err := serveUDP(c.UDPPort, c.UDPResponse)
				if err != nil {
					log.Fatalln(err)
				}
			},
		},
		{
			name: serviceNameICMP,
			serve: func() {
				err := serverICMP()
				if err != nil {
					log.Fatalln(err)
				}
			},
		},
		{
			name: serviceNameProcess,
			serve: func() {
				err := serveProcess(c.ProcessTCPPort, c.ProcessUDPPort)
				if err != nil {
					log.Fatalln(err)
				}
			},
		},
		{
			name: serviceNameProm,
			serve: func() {
				err := serveProm(c.PromPort)
				if err != nil {
					log.Fatalln(err)
				}
			},
		},
	}
}

func tickNormalStatus(interval time.Duration) {
	normal = true
	ticker := time.NewTicker(interval)
	for {
		select {
		case <-ticker.C:
			normalMutex.Lock()
			normal = !normal
			normalMutex.Unlock()
		}
	}
}

func StartTestServer(interval time.Duration, single string, c *TestServerConfig) {
	log.Printf("server config: %+v\n", c)
	for _, s := range getServices(c) {
		if single != "" && s.name != single {
			continue
		}
		log.Println("starting service:", s.name)
		go s.serve()
	}
	go tickNormalStatus(interval)
	for {
		time.Sleep(time.Hour)
	}
}
