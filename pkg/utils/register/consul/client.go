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
	"fmt"
	"os"

	"github.com/hashicorp/consul/api"
)

// ClientOptions supplies explicit in-memory authentication. A nil options value
// retains the SDK's existing environment-based configuration.
type ClientOptions struct {
	Token     string
	Username  string
	Password  string
	TLSConfig api.TLSConfig
}

// NewAPIClient is shared by registration and KV consumers.
func NewAPIClient(address string, options *ClientOptions) (*api.Client, error) {
	conf := api.DefaultConfig()
	conf.Address = address
	if options != nil {
		// api.NewClient also reads defaults, including a token file. Reject these
		// entrances rather than briefly rewriting process-wide environment values.
		for _, key := range []string{"CONSUL_HTTP_TOKEN", "CONSUL_HTTP_TOKEN_FILE", "CONSUL_HTTP_AUTH", "CONSUL_CLIENT_KEY"} {
			if os.Getenv(key) != "" {
				return nil, fmt.Errorf("explicit consul authentication conflicts with %s", key)
			}
		}
		conf.Token, conf.TokenFile = options.Token, ""
		conf.HttpAuth = nil
		if options.Username != "" || options.Password != "" {
			conf.HttpAuth = &api.HttpBasicAuth{Username: options.Username, Password: options.Password}
		}
		conf.TLSConfig = options.TLSConfig
		// Supplying HttpClient prevents NewClient from rebuilding TLS from its
		// implicit defaults after memory-only key material was supplied.
		var err error
		conf.HttpClient, err = api.NewHttpClient(conf.Transport, conf.TLSConfig)
		if err != nil {
			return nil, fmt.Errorf("invalid explicit consul TLS configuration")
		}
	}
	return api.NewClient(conf)
}

// Client client 封装了 consul 部分读写操作
type Client struct {
	agent   *api.Agent
	address string // address IP:Port
}

// NewClient 传入的 address 应符合 IP:Port 的结构，例如: 127.0.0.1:8080
func NewClient(address string) (*Client, error) {
	return NewClientWithOptions(address, nil)
}

func NewClientWithOptions(address string, options *ClientOptions) (*Client, error) {
	client := new(Client)
	client.address = address
	apiClient, err := NewAPIClient(address, options)
	if err != nil {
		return nil, err
	}

	client.agent = apiClient.Agent()
	return client, nil
}

// ServiceDeregister 注销 service, name 既是 ID
func (bc *Client) ServiceDeregister(serviceID string) error {
	return bc.agent.ServiceDeregister(serviceID)
}

func (bc *Client) GetOrCreateService(serviceID, serviceName string, tags []string, address string, port int) error {
	_, options, err := bc.agent.AgentHealthServiceByID(serviceID)
	if options == nil && err == nil {
		option := new(api.AgentServiceRegistration)
		option.Name = serviceName
		option.ID = serviceID
		option.Tags = tags
		option.Address = address
		option.Port = port

		return bc.agent.ServiceRegister(option)
	}

	return nil
}

// CheckRegister 注册检查器
func (bc *Client) CheckRegister(serviceID, checkID string, ttl string) error {
	option := new(api.AgentCheckRegistration)
	option.Name = checkID
	option.TTL = ttl
	option.ServiceID = serviceID

	return bc.agent.CheckRegister(option)
}

// CheckDeregister 注销检查器
func (bc *Client) CheckDeregister(checkID string) error {
	return bc.agent.CheckDeregister(checkID)
}

// CheckFail 标记状态为 fail
func (bc *Client) CheckFail(checkID, note string) error {
	return bc.agent.FailTTL(checkID, note)
}

// CheckPass 标记状态为 pass
func (bc *Client) CheckPass(checkID, note string) error {
	return bc.agent.PassTTL(checkID, note)
}
