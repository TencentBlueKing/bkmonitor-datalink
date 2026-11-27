// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ftaclean

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	scram "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/kafka"
	tc "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type endpoint struct {
	cluster service.ClusterConfig
	auth    service.AuthInfo
	topic   string
}

func readEndpoint(meta *tc.MetaClusterInfo) (endpoint, error) {
	var e endpoint
	if meta == nil {
		return e, fmt.Errorf("missing Kafka configuration")
	}
	for _, part := range []struct{ in, out any }{{meta.ClusterConfig, &e.cluster}, {meta.AuthInfo, &e.auth}} {
		raw, err := json.Marshal(part.in)
		if err != nil {
			return e, err
		}
		if err = json.Unmarshal(raw, part.out); err != nil {
			return e, err
		}
	}
	e.topic, _ = meta.StorageConfig["topic"].(string)
	if e.topic == "" || e.cluster.DomainName == "" || e.cluster.Port == 0 {
		return e, fmt.Errorf("incomplete Kafka endpoint for cluster %d", e.cluster.ClusterId)
	}
	return e, nil
}

func (e endpoint) brokers() []string {
	return []string{net.JoinHostPort(e.cluster.DomainName, strconv.Itoa(int(e.cluster.Port)))}
}

func (e endpoint) settings() (*sarama.Config, error) {
	c := sarama.NewConfig()
	c.Version = sarama.V0_10_2_0
	c.Producer.Return.Successes = true
	c.Producer.RequiredAcks = sarama.WaitForAll
	c.Producer.Partitioner = sarama.NewRandomPartitioner
	if e.auth.Username != "" {
		c.Net.SASL.Enable = true
		c.Net.SASL.User = e.auth.Username
		c.Net.SASL.Password = e.auth.Password
		switch e.auth.SaslMechanisms {
		case "SCRAM-SHA-512", "SCRAM-SHA-256":
			c.Net.SASL.Mechanism = sarama.SASLMechanism(e.auth.SaslMechanisms)
			hash := scram.SHA512
			if e.auth.SaslMechanisms == "SCRAM-SHA-256" {
				hash = scram.SHA256
			}
			c.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient { return &scram.XDGSCRAMClient{HashGeneratorFcn: hash} }
		}
	}
	if e.cluster.IsSslVerify {
		conf := &tls.Config{InsecureSkipVerify: e.cluster.SslInsecureSkipVerify}
		if ca := e.cluster.RawSslCertificateAuthorities; ca != "" {
			conf.RootCAs = x509.NewCertPool()
			if !conf.RootCAs.AppendCertsFromPEM([]byte(ca)) {
				return nil, fmt.Errorf("invalid Kafka CA for cluster %d", e.cluster.ClusterId)
			}
		}
		if e.cluster.RawSslCertificate != "" || e.cluster.RawSslCertificateKey != "" {
			cert, err := tls.X509KeyPair([]byte(e.cluster.RawSslCertificate), []byte(e.cluster.RawSslCertificateKey))
			if err != nil {
				return nil, err
			}
			conf.Certificates = []tls.Certificate{cert}
		}
		c.Net.TLS.Enable = true
		c.Net.TLS.Config = conf
	}
	return c, nil
}

type handler struct {
	pipeline  *pipeline
	producers map[uint]sarama.SyncProducer
}

func (h handler) Setup(sarama.ConsumerGroupSession) error {
	if count := h.pipeline.restarts.recovered(); count > 0 {
		logger.Infof("FTA DataID %d consumer group recovered after %d failures", h.pipeline.id, count)
	}
	return nil
}

func (h handler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (h handler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	ctx := session.Context()
	id := strconv.Itoa(h.pipeline.id)
	partition := strconv.Itoa(int(claim.Partition()))
	return consumeBatches(ctx, claim.Messages(), config.FTAClean.BatchMessages, config.FTAClean.BatchBytes, config.FTAClean.BatchWait, func(batch []*sarama.ConsumerMessage) error {
		start := time.Now()
		defer func() { latency.WithLabelValues(id).Observe(time.Since(start).Seconds()) }()
		groups, err := h.pipeline.clean(ctx, batch)
		if err != nil {
			return err
		}
		for cluster, messages := range groups {
			var failuresLog failureWindow
			for ctx.Err() == nil {
				if err = h.producers[cluster].SendMessages(messages); err == nil {
					if count := failuresLog.recovered(); count > 0 {
						logger.Infof("FTA DataID %d partition %d output cluster %d recovered after %d failures", h.pipeline.id, claim.Partition(), cluster, count)
					}
					outputs.WithLabelValues(id).Add(float64(len(messages)))
					break
				}
				// Sarama identifies the failed messages after all broker replies arrive.
				// Retry that subset so a failing record does not duplicate its siblings.
				if failures, ok := err.(sarama.ProducerErrors); ok && len(failures) > 0 {
					outputs.WithLabelValues(id).Add(float64(len(messages) - len(failures)))
					failed := make(map[*sarama.ProducerMessage]bool, len(failures))
					for _, failure := range failures {
						failed[failure.Msg] = true
					}
					pending := messages[:0]
					for _, msg := range messages {
						if failed[msg] {
							pending = append(pending, msg)
						}
					}
					messages = pending
				}
				writeFailures.WithLabelValues(id).Inc()
				if count := failuresLog.failure(time.Now()); count > 0 {
					logger.Errorf("FTA DataID %d partition %d output cluster %d failed (attempt %d, input offsets %d..%d, pending %d): %v", h.pipeline.id, claim.Partition(), cluster, count, batch[0].Offset, batch[len(batch)-1].Offset, len(messages), err)
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Second):
				}
			}
		}
		if ctx.Err() == nil {
			inputs.WithLabelValues(id).Add(float64(len(batch)))
		}
		return ctx.Err()
	}, func(last *sarama.ConsumerMessage) {
		session.MarkMessage(last, "")
		lag.WithLabelValues(id, partition).Set(float64(claim.HighWaterMarkOffset() - last.Offset - 1))
	})
}

func consume(ctx context.Context, spec *tc.PipelineConfig, p *pipeline) error {
	input, err := readEndpoint(spec.MQConfig)
	if err != nil {
		return err
	}
	sc, err := input.settings()
	if err != nil {
		return err
	}
	if initial, ok := spec.Option[tc.PipelineConfigOptKafkaInitialOffset].(float64); ok && initial < 0 {
		sc.Consumer.Offsets.Initial = int64(initial)
	}
	h := handler{pipeline: p, producers: make(map[uint]sarama.SyncProducer)}
	defer func() {
		for _, prod := range h.producers {
			prod.Close()
		}
	}()
	for _, rt := range spec.ResultTableList {
		for _, ship := range rt.ShipperList {
			e, err := readEndpoint(ship)
			if err != nil {
				return err
			}
			if _, ok := h.producers[e.cluster.ClusterId]; ok {
				continue
			}
			c, err := e.settings()
			if err != nil {
				return err
			}
			prod, err := sarama.NewSyncProducer(e.brokers(), c)
			if err != nil {
				return err
			}
			h.producers[e.cluster.ClusterId] = prod
		}
	}
	group, err := sarama.NewConsumerGroup(input.brokers(), config.FTAClean.GroupPrefix+input.topic, sc)
	if err != nil {
		return err
	}
	defer group.Close()
	for ctx.Err() == nil {
		if err := group.Consume(ctx, []string{input.topic}, h); err != nil {
			return err
		}
	}
	return ctx.Err()
}
