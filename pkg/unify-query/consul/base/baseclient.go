// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package base

import (
	"strings"

	"github.com/hashicorp/consul/api"
	"github.com/hashicorp/consul/api/watch"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
)

// Client 标准consul读写
type Client struct {
	KV      *api.KV
	Agent   *api.Agent
	Session *api.Session

	watchPlanMap       map[string]*watch.Plan
	watchPrefixPlanMap map[string]*watch.Plan

	// 链接相关信息
	// address IP:Port
	address string

	tlsConfig *config.TlsConfig
}

// NewClient 传入的address应符合IP:Port的结构，例如: 127.0.0.1:8080
func NewClient(address string, tlsConfig *config.TlsConfig) (*Client, error) {

	var (
		err    error
		client = &Client{
			address:            address,
			tlsConfig:          tlsConfig,
			watchPlanMap:       make(map[string]*watch.Plan),
			watchPrefixPlanMap: make(map[string]*watch.Plan),
		}
	)

	err = GetAPI(client)
	if err != nil {
		return nil, err
	}

	return client, nil
}

// GetAPI 获取api包中的对象
var GetAPI = func(client *Client) error {

	conf := api.DefaultConfig()

	// 添加链接配置信息
	conf.Address = client.address
	if client.tlsConfig != nil {
		conf.TLSConfig.CAFile = client.tlsConfig.CAFile
		conf.TLSConfig.KeyFile = client.tlsConfig.KeyFile
		conf.TLSConfig.CertFile = client.tlsConfig.CertFile
		conf.TLSConfig.InsecureSkipVerify = client.tlsConfig.SkipVerify
	}

	// 这里的client是api接口的，不是本地的Client，不要搞混了
	apiClient, err := api.NewClient(conf)
	if err != nil {
		return err
	}
	client.Session = apiClient.Session()
	client.KV = apiClient.KV()
	client.Agent = apiClient.Agent()

	return nil
}

// GetPlan 获取监听plan
var GetPlan = func(params map[string]interface{}, outchan chan<- interface{}) (*watch.Plan, error) {
	plan, err := watch.Parse(params)
	if err != nil {
		return nil, err
	}
	plan.Handler = func(num uint64, inter interface{}) {
		outchan <- inter
	}
	return plan, nil
}

// ServiceRegister 注册service,name既是ID
func (bc *Client) ServiceRegister(serviceID, serviceName string, tags []string, address string, port int) error {
	option := new(api.AgentServiceRegistration)

	option.Name = serviceName
	option.Tags = tags
	option.Address = address
	option.Port = port

	option.ID = serviceID

	err := bc.Agent.ServiceRegister(option)
	if err != nil {
		return err
	}
	return nil
}

// ServiceDeregister 注册service,name既是ID
func (bc *Client) ServiceDeregister(serviceID string) error {
	err := bc.Agent.ServiceDeregister(serviceID)
	if err != nil {
		return err
	}
	return nil
}

// ServiceAwake 测试service，若不存在则创建
func (bc *Client) ServiceAwake(serviceID, serviceName string, tags []string, address string, port int) error {
	_, options, err := bc.Agent.AgentHealthServiceByID(serviceID)
	if options == nil && err == nil {
		option := new(api.AgentServiceRegistration)

		option.Name = serviceName
		option.ID = serviceID
		option.Tags = tags
		option.Address = address
		option.Port = port

		err := bc.Agent.ServiceRegister(option)
		if err != nil {
			return err
		}
		return nil
	}
	return nil

}

// CheckRegister 注册check
func (bc *Client) CheckRegister(serviceID, checkID string, ttl string) error {
	option := new(api.AgentCheckRegistration)
	option.Name = checkID
	option.TTL = ttl
	option.ServiceID = serviceID

	err := bc.Agent.CheckRegister(option)
	if err != nil {
		return err
	}

	return nil
}

// CheckDeregister 取消注册
func (bc *Client) CheckDeregister(checkID string) error {

	err := bc.Agent.CheckDeregister(checkID)
	if err != nil {
		return err
	}
	return nil
}

// CheckFail health state修改为fail
func (bc *Client) CheckFail(checkID, note string) error {
	err := bc.Agent.FailTTL(checkID, note)
	if err != nil {
		return err
	}
	return nil
}

// CheckPass health state修改为pass
func (bc *Client) CheckPass(checkID, note string) error {
	err := bc.Agent.PassTTL(checkID, note)
	if err != nil {
		return err
	}
	return nil
}

// CheckStatus :
func (bc *Client) CheckStatus(checkID string) (string, error) {
	checksMap, err := bc.Agent.ChecksWithFilter("Name==" + checkID)
	if err != nil {
		return "", err
	}
	var value string
	res, ok := checksMap[checkID]
	if !ok {
		return "", ErrNoCheckIDFound
	}
	value = res.Status
	return value, nil
}

// formatPrefix 给prefix加分隔符
func (bc *Client) formatPrefix(prefix string, separator string) (string, error) {
	if prefix == "" {
		return "", ErrEmptyPrefix
	}
	if separator == "" {
		separator = "/"
	}
	if !strings.HasSuffix(prefix, separator) {
		prefix = prefix + separator
	}
	return prefix, nil
}

// Put 发送数据
func (bc *Client) Put(path string, value []byte) error {
	var err error
	p := &api.KVPair{Key: path, Value: value}
	_, err = bc.KV.Put(p, nil)
	if err != nil {
		return err
	}
	return nil
}

// Delete 删除数据
func (bc *Client) Delete(path string) error {
	var err error
	_, err = bc.KV.Delete(path, nil)
	if err != nil {
		return err
	}
	return nil
}

// Get 返回指定key的单个value
func (bc *Client) Get(path string) (*api.KVPair, error) {
	var err error
	kvPair, _, err := bc.KV.Get(path, nil)
	if err != nil {
		return nil, err
	}
	return kvPair, nil
}

// GetPrefix 获取指定目录下所有的数据
func (bc *Client) GetPrefix(prefix string, separator string) (api.KVPairs, error) {
	var err error
	prefix, err = bc.formatPrefix(prefix, separator)
	if err != nil {
		return nil, err
	}

	kvPairs, _, err := bc.KV.List(prefix, nil)
	if err != nil {
		return nil, err
	}
	return kvPairs, nil
}

// GetChild 获取下一级子目录的名称
func (bc *Client) GetChild(prefix string, separator string) ([]string, error) {
	var err error
	prefix, err = bc.formatPrefix(prefix, separator)
	if err != nil {
		return nil, err
	}

	childs, _, err := bc.KV.Keys(prefix, separator, nil)
	if err != nil {
		return nil, err
	}
	return childs, nil

}

// makeWatchParams 获取建立监听对象所需要的
func (bc *Client) makeWatchParams(path string, separator string) (map[string]interface{}, error) {
	var params map[string]interface{}
	// 如果没有传入分隔符，则直接监听指定目录
	if separator == "" {
		params = map[string]interface{}{
			"stale": false,
			"type":  "key",
			"key":   path,
		}
		return params, nil
	}
	// 如果传入分隔符，则监听指定目录下所有数据
	prefix, err := bc.formatPrefix(path, separator)
	if err != nil {
		return nil, err
	}
	params = map[string]interface{}{
		"stale":  false,
		"type":   "keyprefix",
		"prefix": prefix,
	}
	return params, nil
}

// addPlanIntoList 将plan添加到列表中
func (bc *Client) addPlanIntoList(plan *watch.Plan, path string, separator string) error {
	if separator == "" {
		bc.watchPlanMap[path] = plan
		return nil
	}
	bc.watchPrefixPlanMap[path] = plan
	return nil

}

// Watch 监听单个地址,返回一个通道用于提供信号
// separator为空字符串，则监听指定的目标位置，若separator不为空,则以其为分隔符做目录监听
func (bc *Client) Watch(path string, separator string) (<-chan interface{}, error) {
	var err error
	var watchParams map[string]interface{}
	watchParams, err = bc.makeWatchParams(path, separator)
	if err != nil {
		return nil, err
	}
	conf := api.DefaultConfig()
	conf.Address = bc.address
	if bc.tlsConfig != nil {
		conf.TLSConfig.InsecureSkipVerify = bc.tlsConfig.SkipVerify
		conf.TLSConfig.CAFile = bc.tlsConfig.CAFile
		conf.TLSConfig.CertFile = bc.tlsConfig.CertFile
		conf.TLSConfig.KeyFile = bc.tlsConfig.KeyFile
	}
	// 若监听到消息则向外输出
	outChan := make(chan interface{})
	// consulAPI提供的监听方案
	plan, err := GetPlan(watchParams, outChan)
	if err != nil {
		return nil, err
	}
	go func() {
		defer func() {
			close(outChan)
		}()
		err1 := plan.RunWithConfig(bc.address, conf)
		if err1 != nil {
			if !plan.IsStopped() {
				plan.Stop()
			}
		}
	}()
	// 添加到队列里进行记录
	err = bc.addPlanIntoList(plan, path, separator)
	if err != nil {
		return nil, err
	}
	return outChan, nil
}

// stopPlan 关闭plan，并且删除列表
func (bc *Client) stopPlan(planMap map[string]*watch.Plan, path string) {
	if !planMap[path].IsStopped() {
		planMap[path].Stop()
	}
	delete(planMap, path)
}

// StopWatch 停止对指定path的监听，plantype: path|prefix
func (bc *Client) StopWatch(path string, planType string) error {
	var planMap map[string]*watch.Plan
	switch planType {
	case "path":
		planMap = bc.watchPlanMap
	case "prefix":
		planMap = bc.watchPrefixPlanMap
	default:
		return ErrWrongPlanType
	}

	// 查找对应plan并关闭
	if _, ok := planMap[path]; ok {
		bc.stopPlan(planMap, path)
	} else {
		return ErrPlanNotFound
	}
	return nil
}

// Close 停止该client下的所有监听
func (bc *Client) Close() error {
	// 关闭所有监听
	for path := range bc.watchPlanMap {
		bc.stopPlan(bc.watchPlanMap, path)
	}
	for path := range bc.watchPrefixPlanMap {
		bc.stopPlan(bc.watchPrefixPlanMap, path)
	}
	return nil
}

// CAS 使用check-and-set的方式写入数据，如果发生错误则error，否则返回true表示写入成功，返回false表示写入失败
func (bc *Client) CAS(path string, preValue []byte, value []byte) (bool, error) {
	// 如果没有前一个值,则直接cas进去
	if preValue == nil {
		kvPair := &api.KVPair{
			Key:   path,
			Value: value,
		}
		result, _, err := bc.KV.CAS(kvPair, nil)
		if err != nil {
			return false, err
		}
		return result, nil
	}
	// 由于对外接口屏蔽了modify_index的概念，所以这里要做重新获取逻辑
	kvPair, _, err := bc.KV.Get(path, nil)
	if err != nil {
		return false, err
	}
	// 如果consul对应key无数据，则会得到nil
	if kvPair == nil {
		kvPair = &api.KVPair{
			Key:   path,
			Value: preValue,
		}
	}
	// 如果数据已发生改变，则等同于cas失败
	if string(kvPair.Value) != string(preValue) {
		return false, nil
	}
	// 赋新值
	kvPair.Value = value
	result, _, err := bc.KV.CAS(kvPair, nil)
	if err != nil {
		return false, err
	}
	return result, nil
}

// NewSessionID 获取一个新session,ttl为过期时间
func (bc *Client) NewSessionID(ttl string) (string, error) {
	entry := &api.SessionEntry{
		TTL: ttl,
	}
	sessionID, _, err := bc.Session.Create(entry, nil)
	if err != nil {
		return "", err
	}
	return sessionID, nil
}

// RenewSession 刷新session，防止过期
func (bc *Client) RenewSession(sessionID string) error {
	_, _, err := bc.Session.Renew(sessionID, nil)
	if err != nil {
		return err
	}
	return nil
}

// Acquire 获取指定位置的锁
func (bc *Client) Acquire(path string, sessionID string) (bool, error) {
	pair := api.KVPair{
		Key:     path,
		Session: sessionID,
	}
	success, _, err := bc.KV.Acquire(&pair, nil)
	return success, err
}

// Release 释放指定位置的锁
func (bc *Client) Release(path string, sessionID string) (bool, error) {
	pair := api.KVPair{
		Key:     path,
		Session: sessionID,
	}
	success, _, err := bc.KV.Release(&pair, nil)
	return success, err
}
