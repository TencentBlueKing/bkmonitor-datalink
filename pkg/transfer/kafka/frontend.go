// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Shopify/sarama"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

var (
	NewKafkaConsumerGroup  = sarama.NewConsumerGroup
	NewKafkaConsumerConfig = func(conf define.Configuration) (*sarama.Config, error) {
		c, err := NewKafkaConfig(conf)
		if err != nil {
			return nil, err
		}

		version, err := sarama.ParseKafkaVersion(conf.GetString(ConfKafkaVersion))
		if err != nil {
			return nil, err
		}
		c.Version = version
		if conf.IsSet(ConfKafkaConsumerOffsetInitial) {
			c.Consumer.Offsets.Initial = conf.GetInt64(ConfKafkaConsumerOffsetInitial)
		}

		return c, c.Validate()
	}
)

const (
	sslVerify                 = "is_ssl_verify"
	sslInsecureSkipVerify     = "ssl_insecure_skip_verify"
	sslCertificateAuthorities = "ssl_certificate_authorities"
	sslCertificate            = "ssl_certificate"
	sslCertificateKey         = "ssl_certificate_key"

	prefixBase64 = "base64://"
)

func decodeSslContent(s string) ([]byte, error) {
	if strings.HasPrefix(s, prefixBase64) {
		return base64.StdEncoding.DecodeString(s[len(prefixBase64):])
	}
	return []byte(s), nil
}

func buildTlsConfig(ctx context.Context) (*tls.Config, bool) {
	mqConf := config.MQConfigFromContext(ctx)
	if mqConf == nil {
		return nil, false
	}
	conf := utils.NewMapHelper(mqConf.ClusterConfig)

	// 判断是否需要 ssl 校验
	verify, _ := conf.GetBool(sslVerify)
	if !verify {
		return nil, false
	}

	caContent, _ := conf.GetString(sslCertificateAuthorities)
	certContent, _ := conf.GetString(sslCertificate)
	certKeyContent, _ := conf.GetString(sslCertificateKey)

	ca, err := decodeSslContent(caContent)
	if err != nil {
		logging.Errorf("failed to decode ssl ca, err: %v", err)
		return nil, false
	}

	cert, err := decodeSslContent(certContent)
	if err != nil {
		logging.Errorf("failed to decode ssl cert, err: %v", err)
		return nil, false
	}

	certKey, err := decodeSslContent(certKeyContent)
	if err != nil {
		logging.Errorf("failed to decode ssl cert key, err: %v", err)
		return nil, false
	}

	// cert / certkey 不能同时为空
	var cas []tls.Certificate
	if len(cert) > 0 || len(certKey) > 0 {
		certPair, err := tls.X509KeyPair(cert, certKey)
		if err != nil {
			logging.Errorf("make ssl cert pair failed, err: %v", err)
			return nil, false
		}
		cas = append(cas, certPair)
	}

	rootCAs := x509.NewCertPool()
	if len(ca) > 0 {
		rootCAs.AppendCertsFromPEM(ca)
	}

	insecureSkipVerify, _ := conf.GetBool(sslInsecureSkipVerify)
	tlsConfig := &tls.Config{
		RootCAs:            rootCAs,
		Certificates:       cas,
		InsecureSkipVerify: insecureSkipVerify,
	}

	return tlsConfig, true
}

// Frontend :
type Frontend struct {
	*define.BaseFrontend
	*define.ProcessorMonitor
	wg             sync.WaitGroup
	ctx            context.Context
	cancelFunc     context.CancelFunc
	group          sarama.ConsumerGroup
	outputChan     chan<- define.Payload
	killChan       chan<- error
	fr             *define.FlowRecorder
	fl             *define.FlowLimiter
	topic          string
	commitInterval time.Duration
	killOnce       uint32 // 确保 kill 信号只会被发送一次
}

// NewFrontend :
func NewFrontend(ctx context.Context, name string) define.Frontend {
	return NewKafkaConsumerGroupFrontend(ctx, name)
}

// NewKafkaConsumerGroupFrontend : client can only re-use but not share
func NewKafkaConsumerGroupFrontend(rootCtx context.Context, name string) *Frontend {
	ctx, cancelFunc := context.WithCancel(rootCtx)
	conf := config.FromContext(ctx)
	kafkaConfig := config.MQConfigFromContext(ctx).AsKafkaCluster()

	rate := kafkaConfig.ConsumeRate
	if rate <= 0 {
		rate = define.DataIdFlowBytes()
	}
	return &Frontend{
		BaseFrontend:     define.NewBaseFrontend(name),
		ProcessorMonitor: pipeline.NewFrontendProcessorMonitor(config.PipelineConfigFromContext(ctx)),
		ctx:              ctx,
		cancelFunc:       cancelFunc,
		commitInterval:   conf.GetDuration(ConfKafkaOffsetsCommitInterval),
		fr:               define.NewFlowRecorder(conf.GetDuration(ConfKafkaFlowInterval)),
		fl:               define.NewFlowLimiter(name, rate),
	}
}

func (f *Frontend) Flow() int {
	return f.fr.Get()
}

func (f *Frontend) Setup(_ sarama.ConsumerGroupSession) error {
	return nil
}

func (f *Frontend) Cleanup(_ sarama.ConsumerGroupSession) error {
	return nil
}

// ConsumeClaim :
func (f *Frontend) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	f.wg.Add(1)
	defer f.wg.Done()

	monitorCounter := MonitorFrontendCommitted.With(prometheus.Labels{"topic": claim.Topic()})
	offsetManager := NewDelayOffsetManager(f.ctx, func(topic string, partition int32, offset int64, metadata string) {
		sess.MarkOffset(topic, partition, offset, metadata)
		monitorCounter.Inc()
	}, claim.Topic(), f.commitInterval)
	defer offsetManager.Close()

loop:
	for {
		select {
		case msg, ok := <-claim.Messages():
			// chaim channel 关闭 直接退出
			if !ok {
				break loop
			}

			logging.Debugf("%v topic:%q partition:%d offset:%d message length:%v", f, msg.Topic, msg.Partition, msg.Offset, len(msg.Value))

			msgLen := len(msg.Value)
			define.LimitRate(msgLen) // 全局流控（确保进程整体不会失控）
			f.fl.Consume(msgLen)     // dataid 流控（确保 dataid 不会失控）
			f.fr.Add(msgLen)         // dataid 流量记录

			payload := f.PayloadCreator()
			err := payload.From(msg.Value)
			if err != nil {
				f.CounterFails.Inc()
				logging.Errorf("decode message from %s failed: %v", msg.Topic, msg.Value)
				continue
			}
			logging.Debugf("%v pulled a message %v from %s", f, payload, msg.Key)
			f.CounterSuccesses.Inc()

			// sent 不成功就 hold 在这里 等到发送成功或者收到 Done 信号
			select {
			case <-f.ctx.Done():
				break loop
			case f.outputChan <- payload:
			}
			offsetManager.Mark(msg) // 成功与否都只消费一次
			offsetManager.RegisterSession(sess)

		case <-f.ctx.Done():
			break loop
		}
	}
	return nil
}

// Pull : pull data
func (f *Frontend) Pull(outputChan chan<- define.Payload, killChan chan<- error) {
	ctx := f.ctx
	defer utils.RecoverError(func(err error) {
		logging.Errorf("frontend %v panic by error: %v", f, err)
	})

	mqConfig := config.MQConfigFromContext(ctx)
	if mqConfig == nil {
		killChan <- errors.Wrapf(define.ErrOperationForbidden, "get frontend %v config failed", f)
		return
	}
	kafkaConfig := mqConfig.AsKafkaCluster()

	err := f.init()
	if err != nil {
		logging.Errorf("frontend %v kill by error %v", f, err)
		killChan <- err
		return
	}

	f.outputChan = outputChan
	f.killChan = killChan

	// blocking
	topic := kafkaConfig.GetTopic()
	f.topic = topic
	rebalanceCounter := MonitorFrontendRebalanced.With(prometheus.Labels{
		"topic": topic,
	})

	go func() {
		defer utils.RecoverError(func(err error) {
			logging.Errorf("frontend %v panic by error: %v", f, err)
		})
	loop:
		for err := range f.group.Errors() {
			if err == nil {
				continue
			}
			logging.Warnf("frontend %v received kafka error %v", f, err)
			if atomic.LoadUint32(&f.killOnce) > 0 {
				break loop
			}
			select {
			case killChan <- err:
				atomic.StoreUint32(&f.killOnce, 1)
			default:
				break loop
			}
		}
		logging.Warnf("frontend %v error handler finished", f)
	}()

loop:
	for {
		logging.Infof("kafka frontend %v consuming topic %s", f, topic)
		err = f.group.Consume(ctx, []string{topic}, f)
		if err != nil {
			logging.Errorf("kafka frontend %v consuming topic %s error %v", f, topic, err)
			if atomic.LoadUint32(&f.killOnce) > 0 {
				return
			}
			atomic.StoreUint32(&f.killOnce, 1)
			killChan <- err
			return
		}
		select {
		case <-ctx.Done():
			break loop
		default:
			// TODO(optimize):如果持续一直 rebalancing 的话证明此时消费组大概率出现故障 应当提前终止 并发送 kill 信号
			logging.Warnf("kafka frontend %v on topic %s is rebalancing, retrying", f, topic)
			// 尝试将 consumergourp 同步至某个时刻统一加入 避免出现一种理论上的混沌状态
			// 假定服务器时间一致的情况
			mod := time.Now().Unix() % 5
			time.Sleep(time.Second * time.Duration(5-mod))
			rebalanceCounter.Inc()
		}
	}
	logging.Infof("kafka frontend %v consuming topic %s finished", f, topic)
}

// Close : close frontend
func (f *Frontend) Close() error {
	f.fr.Stop()
	f.cancelFunc()

	// 可能还没初始化就 Close 判断 group 是否为 nil
	var err error
	if f.group != nil {
		err = f.group.Close()
	}
	f.wg.Wait()
	return err
}

func (f *Frontend) init() error {
	var (
		conf        = config.FromContext(f.ctx)
		kafkaConfig = config.MQConfigFromContext(f.ctx).AsKafkaCluster()
		err         error
		groupPrefix = conf.GetString(ConfKafkaConsumerGroupPrefix)
	)

	pipeConfig := config.PipelineConfigFromContext(f.ctx)
	dataID := strconv.Itoa(pipeConfig.DataID)

	// 由于 dataid 归属的 transfer 集群会发生切换
	// 所以使用时间作为其 values 值，这样查询的时候可以使用 max 语法查询出来
	define.MonitorFrontendKafka.WithLabelValues(dataID, define.ConfClusterID, kafkaConfig.GetDomain(), kafkaConfig.GetTopic()).Set(float64(time.Now().UnixMilli()))

	c, err := NewKafkaConsumerConfig(conf)
	if err != nil {
		logging.Errorf("frontend %v make config error %v", f, err)
		return err
	}

	auth := config.NewAuthInfo(config.MQConfigFromContext(f.ctx))
	userName, err := auth.GetUserName()
	if err != nil {
		logging.Warnf("kafka may not establish connection %v: username", define.ErrGetAuth)
	}
	passWord, err := auth.GetPassword()
	if err != nil {
		logging.Warnf("kafka may not establish connection %v: password", define.ErrGetAuth)
	}

	// 能够正确解析出 tls 则配置上
	tlsConfig, ok := buildTlsConfig(f.ctx)
	if ok {
		c.Net.TLS.Enable = true
		c.Net.TLS.Config = tlsConfig
	}

	if userName != "" || passWord != "" && err == nil {
		c.Net.SASL.User = userName
		c.Net.SASL.Password = passWord
		c.Net.SASL.Enable = true
	}
	logging.Infof("KAFKA MaxProcessTime: %s", c.Consumer.MaxProcessingTime)
	if err != nil {
		logging.Warnf("create kafka config error: %v", err)
		return err
	}

	cluster := fmt.Sprintf("%s:%d", kafkaConfig.GetDomain(), kafkaConfig.GetPort())
	topic := kafkaConfig.GetTopic()
	group := fmt.Sprintf("%s%s", groupPrefix, topic)
	logging.Infof("consuming kafka %s for group %s", cluster, group)
	logging.Debugf("kafka frontend %s config %#v", topic, c)
	f.group, err = NewKafkaConsumerGroup([]string{cluster}, group, c)
	if err != nil {
		logging.Errorf("consume kafka topic %s failed: %v", f.Name, err)
		return err
	}

	return nil
}

func init() {
	define.RegisterFrontend("kafka", func(ctx context.Context, name string) (define.Frontend, error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is nil")
		}
		return NewFrontend(ctx, pipeConfig.FormatName(name)), nil
	})
}
