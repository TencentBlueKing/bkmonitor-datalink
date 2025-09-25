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
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/hashicorp/consul/api"
	"github.com/hashicorp/consul/api/watch"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

var moduleName = "consul_base"

const pathType, prefixType = "path", "prefix"

// BasicClient 标准consul读写
type BasicClient struct {
	KV      KV
	Agent   Agent
	Session Session
	// address IP:Port
	address string

	tlsConfig *config.TlsConfig

	watchPlanMap       map[string]Plan
	watchPrefixPlanMap map[string]Plan
	outChanMap         map[string]chan interface{}
}

// NewBasicClient 传入的address应符合IP:Port的结构，例如: 127.0.0.1:8080
func NewBasicClient(address string, tlsConfig *config.TlsConfig) (ConsulClient, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	var err error
	client := new(BasicClient)
	client.address = address
	if tlsConfig != nil {
		client.tlsConfig = tlsConfig
	}
	err = GetAPI(client)
	if err != nil {
		return nil, err
	}

	client.watchPlanMap = make(map[string]Plan)
	client.watchPrefixPlanMap = make(map[string]Plan)
	client.outChanMap = make(map[string]chan interface{})
	flowLog.Debugf("done")
	return client, nil
}

// GetAPI 获取api包中的对象
var GetAPI = func(client *BasicClient) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	conf := api.DefaultConfig()
	conf.Address = client.address
	if client.tlsConfig != nil {
		conf.TLSConfig.InsecureSkipVerify = client.tlsConfig.SkipVerify
		conf.TLSConfig.CAFile = client.tlsConfig.CAFile
		conf.TLSConfig.CertFile = client.tlsConfig.CertFile
		conf.TLSConfig.KeyFile = client.tlsConfig.KeyFile
	}

	apiClient, err := api.NewClient(conf)
	if err != nil {
		return err
	}
	client.Session = apiClient.Session()
	client.KV = apiClient.KV()
	client.Agent = apiClient.Agent()
	flowLog.Debugf("done")
	return nil
}

// GetPlan 获取监听plan
var GetPlan = func(params map[string]interface{}, outchan chan<- interface{}) (Plan, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called")
	plan, err := watch.Parse(params)
	if err != nil {
		return nil, err
	}
	plan.Handler = func(num uint64, inter interface{}) {
		outchan <- inter
	}
	flowLog.Debugf("done")
	return plan, nil
}

// ServiceRegister 注册service,name既是ID
func (bc *BasicClient) ServiceRegister(serviceName string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called,service ID:%s", serviceName)

	option := new(api.AgentServiceRegistration)
	c := common.Config

	option.Name = serviceName
	option.Tags = []string{"influxdb-proxy"}
	option.Address = c.GetString("http.listen")
	option.Port = c.GetInt("http.port")

	hash := fnv.New32a()
	_, err := hash.Write([]byte(fmt.Sprintf("%s:%d", option.Address, option.Port)))
	if err != nil {
		return err
	}
	option.ID = fmt.Sprintf("%s-influxdb-proxy-%d", serviceName, hash.Sum32())

	err = bc.Agent.ServiceRegister(option)
	if err != nil {
		return err
	}
	flowLog.Debugf("done")
	return nil
}

func (bc *BasicClient) getHashValue() (uint32, error) {
	c := common.Config
	hash := fnv.New32a()
	_, err := hash.Write([]byte(fmt.Sprintf(
		"%s:%d",
		c.GetString("http.listen"), c.GetInt("http.port")),
	))
	if err != nil {
		return 0, err
	}
	return hash.Sum32(), nil
}

// ServiceDeregister 注册service,name既是ID
func (bc *BasicClient) ServiceDeregister(serviceName string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})

	hashValue, err := bc.getHashValue()
	serviceID := fmt.Sprintf("%s-influxdb-proxy-%d", serviceName, hashValue)

	flowLog.Debugf("called,service ID:%s", serviceID)
	err = bc.Agent.ServiceDeregister(serviceID)
	if err != nil {
		return err
	}
	flowLog.Debugf("done")
	return nil
}

// ServiceAwake 测试service，若不存在则创建
func (bc *BasicClient) ServiceAwake(serviceName string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})

	hashValue, err := bc.getHashValue()
	serviceID := fmt.Sprintf("%s-influxdb-proxy-%d", serviceName, hashValue)

	flowLog.Debugf("called,service ID:%s", serviceID)
	res, options, err := bc.Agent.AgentHealthServiceByID(serviceID)
	if options == nil && err == nil {
		flowLog.Debugf("service:%s not exist,init one", serviceID)
		option := new(api.AgentServiceRegistration)

		option.Name = serviceName
		option.ID = serviceID
		c := common.Config
		option.Tags = []string{"influxdb-proxy"}
		option.Address = c.GetString("http.listen")
		option.Port = c.GetInt("http.port")

		err := bc.Agent.ServiceRegister(option)
		if err != nil {
			return err
		}
		flowLog.Debugf("service:%s init done", serviceID)
		return nil
	}
	flowLog.Debugf("service:%s already exist,status:%s", serviceID, res)
	return nil
}

// CheckRegister 注册check
func (bc *BasicClient) CheckRegister(serviceName string, checkID string, ttl string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})

	c := common.Config
	hash := fnv.New32a()
	_, err := hash.Write([]byte(fmt.Sprintf(
		"%s:%d",
		c.GetString("http.listen"), c.GetInt("http.port")),
	))
	if err != nil {
		return err
	}
	serviceID := fmt.Sprintf("%s-influxdb-proxy-%d", serviceName, hash.Sum32())

	flowLog.Debugf("called,service ID:%s,checkID:%s,TTL:%s", serviceID, checkID, ttl)
	option := new(api.AgentCheckRegistration)
	option.Name = checkID
	option.TTL = ttl
	option.ServiceID = serviceID

	err = bc.Agent.CheckRegister(option)
	if err != nil {
		return err
	}

	flowLog.Debugf("service ID: %s check done", serviceName)
	return nil
}

// CheckDeregister 取消注册
func (bc *BasicClient) CheckDeregister(checkID string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called,checkID:%s", checkID)
	err := bc.Agent.CheckDeregister(checkID)
	if err != nil {
		return err
	}
	flowLog.Debugf("done")
	return nil
}

// CheckFail health state修改为fail
func (bc *BasicClient) CheckFail(checkID, note string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called,checkID:%s", checkID)
	err := bc.Agent.FailTTL(checkID, note)
	if err != nil {
		return err
	}
	flowLog.Debugf("done")
	return nil
}

// CheckPass health state修改为pass
func (bc *BasicClient) CheckPass(checkID, note string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called,checkID:%s", checkID)
	err := bc.Agent.PassTTL(checkID, note)
	if err != nil {
		return err
	}
	flowLog.Debugf("done")
	return nil
}

// CheckStatus :
func (bc *BasicClient) CheckStatus(checkID string) (string, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called,checkID:%s", checkID)
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
	flowLog.Debugf("done")
	return value, nil
}

// formatPrefix 给prefix加分隔符
func (bc *BasicClient) formatPrefix(prefix string, separator string) (string, error) {
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
func (bc *BasicClient) Put(path string, value []byte) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	var err error
	p := &api.KVPair{Key: path, Value: value}
	_, err = bc.KV.Put(p, nil)
	if err != nil {
		flowLog.Errorf("put failed,error:%s", err)
		return err
	}
	flowLog.Debugf("done")
	return nil
}

// Delete 删除数据
func (bc *BasicClient) Delete(path string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	var err error
	_, err = bc.KV.Delete(path, nil)
	if err != nil {
		flowLog.Errorf("put failed,error:%s", err)
		return err
	}
	flowLog.Debugf("done")
	return nil
}

// Get 返回指定key的单个value
func (bc *BasicClient) Get(path string) (*api.KVPair, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	var err error
	kvPair, _, err := bc.KV.Get(path, nil)
	if err != nil {
		flowLog.Errorf("get failed,error:%s", err)
		return nil, err
	}
	flowLog.Debugf("done")
	return kvPair, nil
}

// GetPrefix 获取指定目录下所有的数据
func (bc *BasicClient) GetPrefix(prefix string, separator string) (api.KVPairs, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	var err error
	prefix, err = bc.formatPrefix(prefix, separator)
	if err != nil {
		flowLog.Errorf("formatPrefix failed,error:%s", err)
		return nil, err
	}

	kvPairs, _, err := bc.KV.List(prefix, nil)
	if err != nil {
		flowLog.Errorf("get list failed,error:%s", err)
		return nil, err
	}
	flowLog.Debugf("done")
	return kvPairs, nil
}

// GetChild 获取下一级子目录的名称
func (bc *BasicClient) GetChild(prefix string, separator string) ([]string, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	var err error
	prefix, err = bc.formatPrefix(prefix, separator)
	if err != nil {
		flowLog.Errorf("formatPrefix failed,error:%s", err)
		return nil, err
	}

	childs, _, err := bc.KV.Keys(prefix, separator, nil)
	if err != nil {
		flowLog.Errorf("get keys failed,error:%s", err)
		return nil, err
	}
	flowLog.Debugf("done")
	return childs, nil
}

// makeWatchParams 获取建立监听对象所需要的
func (bc *BasicClient) makeWatchParams(path string, separator string) (map[string]interface{}, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called,path:%s,separator:%s", path, separator)
	var params map[string]interface{}
	// 如果没有传入分隔符，则直接监听指定目录
	if separator == "" {
		// 检查是否path是否已在监听
		if _, ok := bc.watchPlanMap[path]; ok {
			return nil, ErrWatchPathRepeat
		}
		// 检查 path 是否已在监听
		params = map[string]interface{}{
			"stale": false,
			"type":  "key",
			"key":   path,
		}
		return params, nil
	}
	// 检查是否path是否已在监听
	if _, ok := bc.watchPrefixPlanMap[path]; ok {
		return nil, ErrWatchPathRepeat
	}
	// 如果传入分隔符，则监听指定目录下所有数据
	prefix, err := bc.formatPrefix(path, separator)
	if err != nil {
		flowLog.Errorf("formatPrefix failed,error:%s", err)
		return nil, err
	}
	params = map[string]interface{}{
		"stale":  false,
		"type":   "keyprefix",
		"prefix": prefix,
	}
	flowLog.Debugf("done")
	return params, nil
}

// addPlanIntoList 将plan添加到列表中
func (bc *BasicClient) addPlanIntoList(plan Plan, path string, separator string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called,path:%s,separator:%s", path, separator)
	if separator == "" {
		bc.watchPlanMap[path] = plan
		flowLog.Debugf("done")
		return nil
	}
	bc.watchPrefixPlanMap[path] = plan
	flowLog.Debugf("done")
	return nil
}

// Watch 监听单个地址,返回一个通道用于提供信号
// separator为空字符串，则监听指定的目标位置，若separator不为空,则以其为分隔符做目录监听
// NOTE: 现阶段如果有多个监听，那么仅有最初的一个拿到数据
func (bc *BasicClient) Watch(path string, separator string) (<-chan interface{}, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	var err error
	var watchParams map[string]interface{}

	conf := api.DefaultConfig()
	conf.Address = bc.address
	if bc.tlsConfig != nil {
		conf.TLSConfig.InsecureSkipVerify = bc.tlsConfig.SkipVerify
		conf.TLSConfig.CAFile = bc.tlsConfig.CAFile
		conf.TLSConfig.CertFile = bc.tlsConfig.CertFile
		conf.TLSConfig.KeyFile = bc.tlsConfig.KeyFile
	}

	// NOTE: 暂时不用考虑删除 集群和 backend 对应路径的场景
	// 1. 检查 path 对应的 plan 是否已经存在
	_, exist := bc.watchPlanMap[path]
	if !exist {
		_, exist = bc.watchPrefixPlanMap[path]
	}
	if exist {
		return bc.outChanMap[path], nil
	}

	// 2. 如果 plan 不存在，则组装参数，启动监听
	// 分类型组装参数
	watchParams, err = bc.makeWatchParams(path, separator)
	if err != nil {
		flowLog.Errorf("make watchParams failed,error:%s", err)
		return nil, err
	}
	// 若监听到消息则向外输出
	outChan := make(chan interface{})
	// consulAPI 提供的监听方案
	plan, err := GetPlan(watchParams, outChan)
	if err != nil {
		flowLog.Errorf("watch parse failed,error:%s", err)
		return nil, err
	}
	// 3. 执行 plan
	go func() {
		defer func() {
			close(outChan)
			flowLog.Debugf("outChan closed")
		}()
		err := plan.RunWithConfig(bc.address, conf)
		if err != nil {
			flowLog.Errorf("plan run failed,error:%s", err)
			if !plan.IsStopped() {
				plan.Stop()
				flowLog.Debugf("plan stopped")
			}
		}
	}()
	// 4. 添加到队列里进行记录
	err = bc.addPlanIntoList(plan, path, separator)
	if err != nil {
		// 如果添加异常，需要停用，然后删除记录
		planType := prefixType
		if separator == "" {
			planType = pathType
		}
		bc.StopWatch(path, planType)
		flowLog.Errorf("addPlanIntoList failed,error:%s", err)
		return nil, err
	}
	// 5. 添加路径和输出的记录
	bc.outChanMap[path] = outChan

	return outChan, nil
}

// stopPlan 关闭plan，并且删除列表
func (bc *BasicClient) stopPlan(planMap map[string]Plan, path string) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Debugf("called,path:%s", path)
	if !planMap[path].IsStopped() {
		planMap[path].Stop()
	}
	delete(planMap, path)
	flowLog.Debugf("done")
}

// StopWatch 停止对指定path的监听，plantype: path|prefix
func (bc *BasicClient) StopWatch(path string, planType string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	var planMap map[string]Plan
	switch planType {
	case pathType:
		planMap = bc.watchPlanMap
	case prefixType:
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
	flowLog.Debugf("done")
	return nil
}

// Close 停止该client下的所有监听
func (bc *BasicClient) Close() error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	// 关闭所有监听
	for path := range bc.watchPlanMap {
		bc.stopPlan(bc.watchPlanMap, path)
	}
	for path := range bc.watchPrefixPlanMap {
		bc.stopPlan(bc.watchPrefixPlanMap, path)
	}
	flowLog.Debugf("done")
	return nil
}

// CAS 使用check-and-set的方式写入数据，如果发生错误则error，否则返回true表示写入成功，返回false表示写入失败
func (bc *BasicClient) CAS(path string, preValue []byte, value []byte) (bool, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
		"path":   path,
	})
	// 如果没有前一个值,则直接cas进去
	if preValue == nil {
		flowLog.Debugf("write new value:%s", value)
		kvPair := &api.KVPair{
			Key:   path,
			Value: value,
		}
		result, _, err := bc.KV.CAS(kvPair, nil)
		if err != nil {
			flowLog.Errorf("write new value failed,error:%s", err)
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
	flowLog.Debugf("compare value,old:%s\tnew:%s", preValue, kvPair.Value)
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
func (bc *BasicClient) NewSessionID(ttl string) (string, error) {
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
func (bc *BasicClient) RenewSession(sessionID string) error {
	_, _, err := bc.Session.Renew(sessionID, nil)
	if err != nil {
		return err
	}
	return nil
}

// Acquire 获取指定位置的锁
func (bc *BasicClient) Acquire(path string, sessionID string) (bool, error) {
	pair := api.KVPair{
		Key:     path,
		Session: sessionID,
	}
	success, _, err := bc.KV.Acquire(&pair, nil)
	return success, err
}

// Release 释放指定位置的锁
func (bc *BasicClient) Release(path string, sessionID string) (bool, error) {
	pair := api.KVPair{
		Key:     path,
		Session: sessionID,
	}
	success, _, err := bc.KV.Release(&pair, nil)
	return success, err
}
