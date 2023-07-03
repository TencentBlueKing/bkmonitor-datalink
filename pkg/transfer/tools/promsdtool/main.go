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
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	consul "github.com/hashicorp/consul/api"
)

type ServiceItem struct {
	Host    string
	Port    string
	Cluster string
}

type Config struct {
	ConsulAddress string
	ServicePrefix string
	ListenAddress string
}

type Controller struct {
	config   Config
	snapshot []ServiceItem
}

func NewController(config Config) *Controller {
	return &Controller{
		config: config,
	}
}

func (c *Controller) syncServices() {
	if err := c.getServiceInfo(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to sync services info: %v\n", err)
	}

	ticker := time.NewTicker(time.Minute)
	for {
		select {
		case <-ticker.C:
			if err := c.getServiceInfo(); err != nil {
				fmt.Fprintf(os.Stderr, "failed to sync services info: %v\n", err)
			}
		}
	}
}

func (c *Controller) getServiceInfo() error {
	config := consul.DefaultConfig()
	config.Address = c.config.ConsulAddress
	client, err := consul.NewClient(consul.DefaultConfig())
	if err != nil {
		return err
	}

	keys, _, err := client.KV().Keys(c.config.ServicePrefix, "/", &consul.QueryOptions{})
	if err != nil {
		return err
	}

	var clusters []string
	for _, key := range keys {
		if !strings.HasPrefix(key, c.config.ServicePrefix) {
			continue
		}
		cluster := key[len(c.config.ServicePrefix):]
		if strings.HasSuffix(cluster, "/") {
			cluster = cluster[:len(cluster)-1]
		}
		clusters = append(clusters, cluster)
	}

	services := make(map[string][]string)
	for _, cluster := range clusters {
		keys, _, err := client.KV().Keys(c.config.ServicePrefix+cluster+"/session/", "/", &consul.QueryOptions{})
		if err != nil {
			return err
		}
		for _, key := range keys {
			services[cluster] = append(services[cluster], key)
		}
	}

	var snapshot []ServiceItem
	for cluster, keys := range services {
		for _, key := range keys {
			si := ServiceItem{Cluster: cluster}
			pair, _, err := client.KV().Get(key+"service_host", &consul.QueryOptions{})
			if err != nil {
				return err
			}
			si.Host = string(pair.Value)

			pair, _, err = client.KV().Get(key+"service_port", &consul.QueryOptions{})
			if err != nil {
				return err
			}
			si.Port = string(pair.Value)
			snapshot = append(snapshot, si)
		}
	}
	c.snapshot = snapshot
	return nil
}

func (c *Controller) routeTargets(w http.ResponseWriter, _ *http.Request) {
	type PromTarget struct {
		Targets []string          `json:"targets"`
		Labels  map[string]string `json:"labels"`
	}

	snapshot := c.snapshot
	targets := make([]PromTarget, 0, len(snapshot))
	for _, item := range snapshot {
		targets = append(targets, PromTarget{
			Targets: []string{item.Host + ":" + item.Port},
			Labels:  map[string]string{"cluster_id": item.Cluster},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	b, _ := json.Marshal(targets)
	w.Write(b)
}

func (c *Controller) Start() error {
	go c.syncServices()

	http.HandleFunc("/targets", c.routeTargets)
	return http.ListenAndServe(c.config.ListenAddress, nil)
}

func main() {
	var config Config

	flag.StringVar(&config.ConsulAddress, "consul-address", "localhost:8500", "consul address")
	flag.StringVar(&config.ListenAddress, "listen-address", "localhost:36080", "http server listen address")
	flag.StringVar(&config.ServicePrefix, "service-prefix", "bk_bkmonitorv3_enterprise_production/service/v1/", "prefix of transfer service path")
	flag.Parse()

	controller := NewController(config)
	if err := controller.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start controller: %v\n", err)
	}
}
