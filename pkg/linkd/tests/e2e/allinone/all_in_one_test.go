// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package allinone_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	redis "github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"linkd/internal/cleaner"
	"linkd/internal/domain"
	"linkd/internal/lifecycle/kafkahook"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/store"
	"linkd/internal/testkit/rawgen"
)

const (
	e2eEnabledEnv          = "LINKD_E2E"
	elasticsearchURLEnv    = "LINKD_E2E_ELASTICSEARCH_URL"
	redisAddressEnv        = "LINKD_E2E_REDIS_ADDRESS"
	redisPasswordEnv       = "LINKD_E2E_REDIS_PASSWORD" //nolint:gosec // G101: 这是环境变量名，不是凭据值。
	redisDatabaseEnv       = "LINKD_E2E_REDIS_DATABASE"
	kafkaBrokerEnv         = "LINKD_E2E_KAFKA_BROKER"
	mysqlAddressEnv        = "LINKD_E2E_MYSQL_ADDRESS"
	mysqlUsernameEnv       = "LINKD_E2E_MYSQL_USERNAME"
	mysqlPasswordEnv       = "LINKD_E2E_MYSQL_PASSWORD" //nolint:gosec // G101: 这是环境变量名，不是凭据值。
	profileEnv             = "LINKD_E2E_PROFILE"
	defaultE2ETimeout      = 90 * time.Second
	processShutdownTimeout = 20 * time.Second
	e2eTopicPartitions     = 3
)

type e2eEnvironment struct {
	ElasticsearchURL string
	RedisAddress     string
	RedisPassword    string
	RedisDatabase    int
	KafkaBroker      string
	MySQLAddress     string
	MySQLUsername    string
	MySQLPassword    string
}

type resourceNames struct {
	Token                string
	IndexPrefix          string
	EventIndex           string
	AlertIndex           string
	AlertLogIndex        string
	RawTopic             string
	OutputTopic          string
	CleanerGroup         string
	OutputGroup          string
	SignalStream         string
	SignalGroup          string
	SignalConsumerPrefix string
	LockPrefix           string
	OutputClientID       string
	MySQLDatabase        string
}

type elasticsearchClient struct {
	baseURL *url.URL
	client  *http.Client
}

type linkdProcess struct {
	command *exec.Cmd
	wait    chan error
	exited  bool
	exitErr error
	logFile *os.File
	logPath string
}

func TestAllInOneElasticsearchE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skipf("set %s=1 to run external-service E2E", e2eEnabledEnv)
	}
	repoRoot := repositoryRoot(t)
	environment := loadEnvironment(t)
	names := newResourceNames()
	dataset := loadGeneratedDataset(t, repoRoot)
	expected := dataset.Expected
	ctx, cancel := context.WithTimeout(context.Background(), defaultE2ETimeout)
	defer cancel()

	es := newElasticsearchClient(t, environment.ElasticsearchURL)
	version := es.version(ctx, t)
	if version != "7.17.7" {
		t.Fatalf("Elasticsearch version = %q, want 7.17.7", version)
	}
	t.Cleanup(func() { cleanupElasticsearch(t, es, names) })

	redisClient := redis.NewClient(&redis.Options{
		Addr: environment.RedisAddress, Password: environment.RedisPassword, DB: environment.RedisDatabase,
	})
	if err := redisClient.Ping(ctx).Err(); err != nil {
		t.Fatalf("connect Redis: %v", err)
	}
	t.Cleanup(func() {
		cleanupRedis(t, redisClient, names)
		if err := redisClient.Close(); err != nil {
			t.Logf("close Redis client: %v", err)
		}
	})

	kafkaAdmin := newKafkaClient(t, environment.KafkaBroker, "linkd-e2e-admin-"+names.Token)
	if err := kafkaAdmin.Ping(ctx); err != nil {
		t.Fatalf("connect Kafka: %v", err)
	}
	createTopics(ctx, t, kafkaAdmin, names.RawTopic, names.OutputTopic)
	t.Cleanup(func() {
		cleanupKafkaTopics(t, kafkaAdmin, names.RawTopic, names.OutputTopic)
		kafkaAdmin.Close()
	})

	temporaryDirectory := t.TempDir()
	binaryPath := filepath.Join(temporaryDirectory, "linkd")
	buildLinkd(t, ctx, repoRoot, binaryPath)
	configPath := writeConfig(
		t,
		repoRoot,
		temporaryDirectory,
		"config.elasticsearch.template.yaml",
		environment,
		names,
		dataset.Config.EventSourceID,
		nil,
	)

	process := startAllInOne(t, repoRoot, binaryPath, configPath, temporaryDirectory)
	t.Cleanup(func() {
		if err := process.stop(); err != nil {
			t.Logf("stop all-in-one during cleanup: %v", err)
		}
		if t.Failed() {
			if data, err := os.ReadFile(process.logPath); err == nil {
				t.Logf("all-in-one log:\n%s", data)
			}
		}
	})
	waitUntilReady(ctx, t, process, es, redisClient, names)

	produceDataset(ctx, t, environment.KafkaBroker, names.RawTopic, dataset)
	events := waitForAcceptedEvents(ctx, t, process, es, names.EventIndex, expected)
	assertEvents(t, events, dataset)
	alerts := loadAlerts(ctx, t, es, names.AlertIndex)
	assertAlerts(t, alerts, expected.Alerts)
	logs := waitForAlertLogsVisible(ctx, t, process, es, names.AlertLogIndex, expected.OperationCounts)
	assertAlertLogs(t, logs, expected.OperationCounts)
	outputs := consumeOutputs(ctx, t, environment.KafkaBroker, names, expected.OutputMessages)
	assertOutputs(t, outputs, events, expected.OutputMessages)
	waitForRedisDrain(ctx, t, redisClient, names)

	if err := process.stop(); err != nil {
		t.Fatalf("gracefully stop all-in-one: %v", err)
	}
	t.Logf(
		"E2E passed: backend=elasticsearch version=%s input=%d events=%d alerts=%d logs=%d output=%d stream=%s",
		version,
		expected.InputRecords,
		len(events),
		len(alerts),
		len(logs),
		len(outputs),
		names.SignalStream,
	)
}

func waitForAlertLogsVisible(
	ctx context.Context,
	t *testing.T,
	process *linkdProcess,
	es *elasticsearchClient,
	index string,
	expected map[domain.OperationKind]int,
) []domain.AlertLog {
	t.Helper()
	want := 0
	for _, count := range expected {
		want += count
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if exited, err := process.checkExited(); exited {
			t.Fatalf("all-in-one exited while waiting for AlertLog refresh: %v", err)
		}
		logs := loadAlertLogs(ctx, t, es, index)
		if len(logs) >= want {
			return logs
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for AlertLog refresh: found %d/%d: %v", len(logs), want, ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestAllInOneMySQLE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skipf("set %s=1 to run external-service E2E", e2eEnabledEnv)
	}
	repoRoot := repositoryRoot(t)
	environment := loadEnvironment(t)
	names := newResourceNames()
	dataset := loadGeneratedDataset(t, repoRoot)
	expected := dataset.Expected
	ctx, cancel := context.WithTimeout(context.Background(), defaultE2ETimeout)
	defer cancel()

	adminDatabase, repositoryDatabase, version := createMySQLDatabase(ctx, t, environment, names.MySQLDatabase)
	if !strings.HasPrefix(version, "8.4.10") {
		t.Fatalf("MySQL version = %q, want 8.4.10", version)
	}
	t.Cleanup(func() {
		cleanupMySQLDatabase(t, adminDatabase, repositoryDatabase, names.MySQLDatabase)
	})

	redisClient := redis.NewClient(&redis.Options{
		Addr: environment.RedisAddress, Password: environment.RedisPassword, DB: environment.RedisDatabase,
	})
	if err := redisClient.Ping(ctx).Err(); err != nil {
		t.Fatalf("connect Redis: %v", err)
	}
	t.Cleanup(func() {
		cleanupRedis(t, redisClient, names)
		if err := redisClient.Close(); err != nil {
			t.Logf("close Redis client: %v", err)
		}
	})

	kafkaAdmin := newKafkaClient(t, environment.KafkaBroker, "linkd-e2e-admin-"+names.Token)
	if err := kafkaAdmin.Ping(ctx); err != nil {
		t.Fatalf("connect Kafka: %v", err)
	}
	createTopics(ctx, t, kafkaAdmin, names.RawTopic, names.OutputTopic)
	t.Cleanup(func() {
		cleanupKafkaTopics(t, kafkaAdmin, names.RawTopic, names.OutputTopic)
		kafkaAdmin.Close()
	})

	temporaryDirectory := t.TempDir()
	binaryPath := filepath.Join(temporaryDirectory, "linkd")
	buildLinkd(t, ctx, repoRoot, binaryPath)
	configPath := writeConfig(
		t,
		repoRoot,
		temporaryDirectory,
		"config.mysql.template.yaml",
		environment,
		names,
		dataset.Config.EventSourceID,
		map[string]string{
			"{{MYSQL_ADDRESS}}":  environment.MySQLAddress,
			"{{MYSQL_DATABASE}}": names.MySQLDatabase,
			"{{MYSQL_USERNAME}}": environment.MySQLUsername,
			"{{MYSQL_PASSWORD}}": environment.MySQLPassword,
		},
	)

	process := startAllInOne(t, repoRoot, binaryPath, configPath, temporaryDirectory)
	t.Cleanup(func() {
		if err := process.stop(); err != nil {
			t.Logf("stop all-in-one during cleanup: %v", err)
		}
		if t.Failed() {
			if data, err := os.ReadFile(process.logPath); err == nil {
				t.Logf("all-in-one log:\n%s", data)
			}
		}
	})
	waitUntilMySQLReady(ctx, t, process, repositoryDatabase, redisClient, names)

	produceDataset(ctx, t, environment.KafkaBroker, names.RawTopic, dataset)
	events := waitForAcceptedMySQLEvents(ctx, t, process, repositoryDatabase, expected)
	assertEvents(t, events, dataset)
	alerts := loadMySQLAlerts(ctx, t, repositoryDatabase)
	assertAlerts(t, alerts, expected.Alerts)
	logs := loadMySQLAlertLogs(ctx, t, repositoryDatabase)
	assertAlertLogs(t, logs, expected.OperationCounts)
	outputs := consumeOutputs(ctx, t, environment.KafkaBroker, names, expected.OutputMessages)
	assertOutputs(t, outputs, events, expected.OutputMessages)
	waitForRedisDrain(ctx, t, redisClient, names)

	if err := process.stop(); err != nil {
		t.Fatalf("gracefully stop all-in-one: %v", err)
	}
	t.Logf(
		"E2E passed: backend=mysql version=%s input=%d events=%d alerts=%d logs=%d output=%d database=%s",
		version,
		expected.InputRecords,
		len(events),
		len(alerts),
		len(logs),
		len(outputs),
		names.MySQLDatabase,
	)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "../../.."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func loadEnvironment(t *testing.T) e2eEnvironment {
	t.Helper()
	database, err := strconv.Atoi(envOrDefault(redisDatabaseEnv, "0"))
	if err != nil || database < 0 {
		t.Fatalf("%s must be a non-negative integer", redisDatabaseEnv)
	}
	return e2eEnvironment{
		ElasticsearchURL: envOrDefault(elasticsearchURLEnv, "http://127.0.0.1:9200"),
		RedisAddress:     envOrDefault(redisAddressEnv, "127.0.0.1:16379"),
		RedisPassword:    envOrDefault(redisPasswordEnv, "test123456"),
		RedisDatabase:    database,
		KafkaBroker:      envOrDefault(kafkaBrokerEnv, "127.0.0.1:9092"),
		MySQLAddress:     envOrDefault(mysqlAddressEnv, "127.0.0.1:13306"),
		MySQLUsername:    envOrDefault(mysqlUsernameEnv, "root"),
		MySQLPassword:    envOrDefault(mysqlPasswordEnv, "test123456"),
	}
}

func envOrDefault(name, fallback string) string {
	if value, exists := os.LookupEnv(name); exists {
		return value
	}
	return fallback
}

func newResourceNames() resourceNames {
	token := strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.Itoa(os.Getpid())
	prefix := "linkd-e2e-" + token
	return resourceNames{
		Token:                token,
		IndexPrefix:          prefix,
		EventIndex:           prefix + "-events",
		AlertIndex:           prefix + "-alerts",
		AlertLogIndex:        prefix + "-alert-logs",
		RawTopic:             prefix + "-raw",
		OutputTopic:          prefix + "-output",
		CleanerGroup:         prefix + "-cleaner",
		OutputGroup:          prefix + "-assert-output",
		SignalStream:         "linkd:e2e:" + token + ":signals",
		SignalGroup:          prefix + "-lifecycle",
		SignalConsumerPrefix: prefix + "-consumer",
		LockPrefix:           "linkd:e2e:" + token + ":lock",
		OutputClientID:       prefix + "-hook",
		MySQLDatabase:        strings.ReplaceAll(prefix, "-", "_"),
	}
}

func loadGeneratedDataset(t *testing.T, root string) rawgen.Dataset {
	t.Helper()
	profilePath := envOrDefault(profileEnv, filepath.Join(root, "tests/e2e/allinone/testdata/profile.json"))
	config, err := rawgen.LoadConfig(profilePath)
	if err != nil {
		t.Fatalf("load E2E generation profile: %v", err)
	}
	dataset, err := rawgen.Generate(config)
	if err != nil {
		t.Fatalf("generate E2E dataset: %v", err)
	}
	return dataset
}

func createMySQLDatabase(
	ctx context.Context,
	t *testing.T,
	environment e2eEnvironment,
	databaseName string,
) (*sql.DB, *sql.DB, string) {
	t.Helper()
	if !validMySQLDatabaseName(databaseName) {
		t.Fatalf("generated MySQL database name is invalid: %q", databaseName)
	}
	adminConfig := driver.NewConfig()
	adminConfig.User = environment.MySQLUsername
	adminConfig.Passwd = environment.MySQLPassword
	adminConfig.Net = "tcp"
	adminConfig.Addr = environment.MySQLAddress
	adminConfig.DBName = "mysql"
	adminConfig.ParseTime = true
	adminConfig.Loc = time.UTC
	adminDatabase, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatalf("open MySQL admin connection: %v", err)
	}
	if err := adminDatabase.PingContext(ctx); err != nil {
		_ = adminDatabase.Close()
		t.Fatalf("connect MySQL admin: %v", err)
	}
	var version string
	if err := adminDatabase.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		_ = adminDatabase.Close()
		t.Fatalf("read MySQL version: %v", err)
	}
	// databaseName 只含经过上方封闭校验的 ASCII 字母、数字和下划线。
	if _, err := adminDatabase.ExecContext(
		ctx,
		"CREATE DATABASE `"+databaseName+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin",
	); err != nil {
		_ = adminDatabase.Close()
		t.Fatalf("create isolated MySQL database %q: %v", databaseName, err)
	}
	repositoryConfig := *adminConfig
	repositoryConfig.DBName = databaseName
	repositoryDatabase, err := sql.Open("mysql", repositoryConfig.FormatDSN())
	if err != nil {
		cleanupMySQLDatabase(t, adminDatabase, nil, databaseName)
		t.Fatalf("open isolated MySQL database: %v", err)
	}
	if err := repositoryDatabase.PingContext(ctx); err != nil {
		cleanupMySQLDatabase(t, adminDatabase, repositoryDatabase, databaseName)
		t.Fatalf("connect isolated MySQL database: %v", err)
	}
	return adminDatabase, repositoryDatabase, version
}

func cleanupMySQLDatabase(t *testing.T, adminDatabase, repositoryDatabase *sql.DB, databaseName string) {
	t.Helper()
	if repositoryDatabase != nil {
		if err := repositoryDatabase.Close(); err != nil {
			t.Logf("close E2E MySQL repository connection: %v", err)
		}
	}
	if adminDatabase == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if validMySQLDatabaseName(databaseName) {
		// databaseName 只含由测试生成并再次校验的 ASCII 字母、数字和下划线。
		if _, err := adminDatabase.ExecContext(ctx, "DROP DATABASE IF EXISTS `"+databaseName+"`"); err != nil {
			t.Logf("drop E2E MySQL database %q: %v", databaseName, err)
		}
	}
	if err := adminDatabase.Close(); err != nil {
		t.Logf("close E2E MySQL admin connection: %v", err)
	}
}

func validMySQLDatabaseName(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func newElasticsearchClient(t *testing.T, endpoint string) *elasticsearchClient {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		t.Fatalf("invalid Elasticsearch URL %q", endpoint)
	}
	return &elasticsearchClient{baseURL: parsed, client: &http.Client{Timeout: 10 * time.Second}}
}

func (c *elasticsearchClient) do(
	ctx context.Context,
	method, path string,
	body []byte,
	result any,
) (int, error) {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimSuffix(c.baseURL.Path, "/") + path
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	if len(body) != 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return response.StatusCode, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return response.StatusCode, fmt.Errorf("Elasticsearch %s %s returned %d: %s", method, path, response.StatusCode, data)
	}
	if result != nil && len(bytes.TrimSpace(data)) != 0 {
		if err := json.Unmarshal(data, result); err != nil {
			return response.StatusCode, err
		}
	}
	return response.StatusCode, nil
}

func (c *elasticsearchClient) version(ctx context.Context, t *testing.T) string {
	t.Helper()
	var response struct {
		Version struct {
			Number string `json:"number"`
		} `json:"version"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/", nil, &response); err != nil {
		t.Fatalf("connect Elasticsearch: %v", err)
	}
	return response.Version.Number
}

func cleanupElasticsearch(t *testing.T, client *elasticsearchClient, names resourceNames) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, path := range []string{
		"/" + names.IndexPrefix + "-*",
		"/_index_template/" + names.IndexPrefix + "-events-template",
		"/_index_template/" + names.IndexPrefix + "-alerts-active-template",
		"/_index_template/" + names.IndexPrefix + "-alert-history-template",
		"/_index_template/" + names.IndexPrefix + "-alert-logs-template",
	} {
		status, err := client.do(ctx, http.MethodDelete, path, nil, nil)
		if err != nil && status != http.StatusNotFound {
			t.Logf("cleanup Elasticsearch %s: %v", path, err)
		}
	}
}

func cleanupRedis(t *testing.T, client *redis.Client, names resourceNames) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	keys := []string{names.SignalStream}
	var cursor uint64
	for {
		found, next, err := client.Scan(ctx, cursor, names.LockPrefix+"*", 100).Result()
		if err != nil {
			t.Logf("scan E2E Redis locks: %v", err)
			break
		}
		keys = append(keys, found...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	if err := client.Del(ctx, keys...).Err(); err != nil {
		t.Logf("cleanup E2E Redis keys: %v", err)
	}
}

func newKafkaClient(t *testing.T, broker, clientID string) *kgo.Client {
	t.Helper()
	client, err := kgo.NewClient(kgo.SeedBrokers(broker), kgo.ClientID(clientID))
	if err != nil {
		t.Fatalf("create Kafka client: %v", err)
	}
	return client
}

func createTopics(ctx context.Context, t *testing.T, client *kgo.Client, topics ...string) {
	t.Helper()
	request := kmsg.NewPtrCreateTopicsRequest()
	for _, topic := range topics {
		request.Topics = append(request.Topics, kmsg.CreateTopicsRequestTopic{
			Topic: topic, NumPartitions: e2eTopicPartitions, ReplicationFactor: 1,
		})
	}
	response, err := client.Request(ctx, request)
	if err != nil {
		t.Fatalf("create Kafka topics: %v", err)
	}
	created, ok := response.(*kmsg.CreateTopicsResponse)
	if !ok {
		t.Fatalf("create Kafka topics returned %T", response)
	}
	for _, topic := range created.Topics {
		if topic.ErrorCode != 0 {
			t.Fatalf("create Kafka topic %q: %v", topic.Topic, kerr.ErrorForCode(topic.ErrorCode))
		}
	}
}

func cleanupKafkaTopics(t *testing.T, client *kgo.Client, topics ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request := newDeleteTopicsRequest(topics)
	response, err := client.Request(ctx, request)
	if err != nil {
		t.Errorf("cleanup Kafka topics: %v", err)
		return
	}
	deleted, ok := response.(*kmsg.DeleteTopicsResponse)
	if !ok {
		t.Errorf("cleanup Kafka topics returned %T", response)
		return
	}
	for _, topic := range deleted.Topics {
		if topic.ErrorCode != 0 && !errors.Is(kerr.ErrorForCode(topic.ErrorCode), kerr.UnknownTopicOrPartition) {
			topicName := "<unknown>"
			if topic.Topic != nil {
				topicName = *topic.Topic
			}
			t.Errorf("cleanup Kafka topic %q: %v", topicName, kerr.ErrorForCode(topic.ErrorCode))
			return
		}
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		remaining, metadataErr := remainingKafkaTopics(ctx, client, topics)
		if metadataErr != nil {
			t.Errorf("verify Kafka topic cleanup: %v", metadataErr)
			return
		}
		if len(remaining) == 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Errorf("Kafka topics were not deleted before timeout: %v", remaining)
			return
		case <-ticker.C:
		}
	}
}

func newDeleteTopicsRequest(topics []string) *kmsg.DeleteTopicsRequest {
	request := kmsg.NewPtrDeleteTopicsRequest()
	request.TopicNames = append([]string(nil), topics...)
	for _, topic := range topics {
		topicName := topic
		request.Topics = append(request.Topics, kmsg.DeleteTopicsRequestTopic{Topic: &topicName})
	}
	request.TimeoutMillis = 10_000
	return request
}

func remainingKafkaTopics(ctx context.Context, client *kgo.Client, topics []string) ([]string, error) {
	wanted := make(map[string]struct{}, len(topics))
	for _, topic := range topics {
		wanted[topic] = struct{}{}
	}
	response, err := client.Request(ctx, kmsg.NewPtrMetadataRequest())
	if err != nil {
		return nil, err
	}
	metadata, ok := response.(*kmsg.MetadataResponse)
	if !ok {
		return nil, fmt.Errorf("Kafka metadata returned %T", response)
	}
	remaining := make([]string, 0, len(topics))
	for _, topic := range metadata.Topics {
		if topic.Topic == nil {
			continue
		}
		if _, exists := wanted[*topic.Topic]; exists &&
			!errors.Is(kerr.ErrorForCode(topic.ErrorCode), kerr.UnknownTopicOrPartition) {
			remaining = append(remaining, *topic.Topic)
		}
	}
	sort.Strings(remaining)
	return remaining, nil
}

func TestDeleteTopicsRequestSupportsLegacyAndCurrentProtocolVersions(t *testing.T) {
	t.Parallel()
	request := newDeleteTopicsRequest([]string{"raw-topic", "output-topic"})
	if !reflect.DeepEqual(request.TopicNames, []string{"raw-topic", "output-topic"}) {
		t.Fatalf("legacy topic names = %v", request.TopicNames)
	}
	if len(request.Topics) != 2 || request.Topics[0].Topic == nil || *request.Topics[0].Topic != "raw-topic" ||
		request.Topics[1].Topic == nil || *request.Topics[1].Topic != "output-topic" {
		t.Fatalf("current protocol topics = %#v", request.Topics)
	}
}

func buildLinkd(t *testing.T, ctx context.Context, root, output string) {
	t.Helper()
	// output 来自 t.TempDir，命令和源码目标均为固定值。
	//nolint:gosec // G204: 仅调用 Go 工具链构建当前仓库固定入口。
	command := exec.CommandContext(ctx, "go", "build", "-o", output, "./cmd/linkd")
	command.Dir = root
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build linkd: %v\n%s", err, data)
	}
}

func writeConfig(
	t *testing.T,
	root, directory, templateName string,
	environment e2eEnvironment,
	names resourceNames,
	eventSourceID string,
	extra map[string]string,
) string {
	t.Helper()
	// root 由当前测试源文件的绝对路径推导，不接受调用方输入。
	//nolint:gosec // G304: 测试只读取仓库内固定配置模板。
	template, err := os.ReadFile(filepath.Join(root, "tests/e2e/allinone", templateName))
	if err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{
		"{{ELASTICSEARCH_URL}}":      environment.ElasticsearchURL,
		"{{INDEX_PREFIX}}":           names.IndexPrefix,
		"{{REDIS_ADDRESS}}":          environment.RedisAddress,
		"{{REDIS_PASSWORD}}":         environment.RedisPassword,
		"{{REDIS_DATABASE}}":         strconv.Itoa(environment.RedisDatabase),
		"{{SIGNAL_STREAM}}":          names.SignalStream,
		"{{SIGNAL_GROUP}}":           names.SignalGroup,
		"{{SIGNAL_CONSUMER_PREFIX}}": names.SignalConsumerPrefix,
		"{{LOCK_PREFIX}}":            names.LockPrefix,
		"{{KAFKA_BROKER}}":           environment.KafkaBroker,
		"{{OUTPUT_TOPIC}}":           names.OutputTopic,
		"{{OUTPUT_CLIENT_ID}}":       names.OutputClientID,
		"{{RAW_TOPIC}}":              names.RawTopic,
		"{{CLEANER_GROUP}}":          names.CleanerGroup,
		"{{EVENT_SOURCE_ID}}":        eventSourceID,
	}
	for placeholder, value := range extra {
		replacements[placeholder] = value
	}
	configText := string(template)
	for placeholder, value := range replacements {
		configText = strings.ReplaceAll(configText, placeholder, value)
	}
	if strings.Contains(configText, "{{") {
		t.Fatal("E2E config contains unresolved placeholders")
	}
	path := filepath.Join(directory, "linkd-e2e.yaml")
	// path 位于 testing.T.TempDir 创建的隔离目录中。
	//nolint:gosec // G703: 不包含外部可控路径片段。
	if err := os.WriteFile(path, []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func startAllInOne(
	t *testing.T,
	root, binaryPath, configPath, directory string,
) *linkdProcess {
	t.Helper()
	logPath := filepath.Join(directory, "all-in-one.log")
	// logPath 位于 testing.T.TempDir 创建的隔离目录中。
	//nolint:gosec // G304: 不包含外部可控路径片段。
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	// binaryPath 和 configPath 都位于本测试创建的临时目录。
	//nolint:gosec // G204: 只启动刚构建的 Linkd 固定子命令。
	command := exec.CommandContext(context.Background(), binaryPath, "run", "all-in-one", "--config", configPath)
	command.Dir = root
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		t.Fatalf("start linkd run all-in-one: %v", err)
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	return &linkdProcess{command: command, wait: wait, logFile: logFile, logPath: logPath}
}

func (p *linkdProcess) checkExited() (bool, error) {
	if p.exited {
		return true, p.exitErr
	}
	select {
	case err := <-p.wait:
		p.exited = true
		p.exitErr = err
		_ = p.logFile.Close()
		return true, err
	default:
		return false, nil
	}
}

func (p *linkdProcess) stop() error {
	if exited, err := p.checkExited(); exited {
		return err
	}
	if err := p.command.Process.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	select {
	case err := <-p.wait:
		p.exited = true
		p.exitErr = err
		_ = p.logFile.Close()
		return err
	case <-time.After(processShutdownTimeout):
		killErr := p.command.Process.Kill()
		err := <-p.wait
		p.exited = true
		p.exitErr = errors.Join(err, killErr, fmt.Errorf("all-in-one shutdown timed out"))
		_ = p.logFile.Close()
		return p.exitErr
	}
}

func waitUntilReady(
	ctx context.Context,
	t *testing.T,
	process *linkdProcess,
	es *elasticsearchClient,
	redisClient *redis.Client,
	names resourceNames,
) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if exited, err := process.checkExited(); exited {
			t.Fatalf("all-in-one exited before ready: %v", err)
		}
		indicesReady := true
		for _, index := range []string{names.EventIndex, names.AlertIndex, names.AlertLogIndex} {
			status, err := es.do(ctx, http.MethodHead, "/"+index, nil, nil)
			if err != nil || status != http.StatusOK {
				indicesReady = false
				break
			}
		}
		groups, err := redisClient.XInfoGroups(ctx, names.SignalStream).Result()
		groupReady := err == nil && len(groups) == 1 && groups[0].Name == names.SignalGroup
		if indicesReady && groupReady {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for all-in-one readiness: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitUntilMySQLReady(
	ctx context.Context,
	t *testing.T,
	process *linkdProcess,
	database *sql.DB,
	redisClient *redis.Client,
	names resourceNames,
) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if exited, err := process.checkExited(); exited {
			t.Fatalf("all-in-one exited before MySQL ready: %v", err)
		}
		var tableCount int
		tableErr := database.QueryRowContext(
			ctx,
			`SELECT COUNT(*)
			   FROM information_schema.tables
			  WHERE table_schema = DATABASE()
			    AND table_name IN ('linkd_events', 'linkd_alerts', 'linkd_alert_logs')`,
		).Scan(&tableCount)
		groups, groupErr := redisClient.XInfoGroups(ctx, names.SignalStream).Result()
		groupReady := groupErr == nil && len(groups) == 1 && groups[0].Name == names.SignalGroup
		if tableErr == nil && tableCount == 3 && groupReady {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf(
				"wait for MySQL all-in-one readiness: tables=%d table_err=%v groups=%v group_err=%v: %v",
				tableCount,
				tableErr,
				groups,
				groupErr,
				ctx.Err(),
			)
		case <-ticker.C:
		}
	}
}

func produceDataset(ctx context.Context, t *testing.T, broker, topic string, dataset rawgen.Dataset) {
	t.Helper()
	producer, err := kgo.NewClient(
		kgo.SeedBrokers(broker),
		kgo.ClientID("linkd-e2e-producer"),
		kgo.RequiredAcks(kgo.AllISRAcks()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()

	for index, record := range dataset.Records {
		message := &kgo.Record{
			Topic: topic, Key: []byte(record.KafkaKey), Value: record.Body, Timestamp: record.KafkaTimestamp,
		}
		if record.Valid {
			raw, decodeErr := (cleaner.StandardCleaner{}).Clean(ctx, cleaner.RawEventMessage{Payload: record.Body})
			if decodeErr != nil {
				t.Fatalf("decode generated valid standard event %d: %v", index, decodeErr)
			}
			message.Headers = []kgo.RecordHeader{
				{Key: "message_id", Value: []byte("record-" + raw.SourceEventID)},
				{Key: "bk_tenant_id", Value: []byte(record.BKTenantID)},
				{
					Key: "order_key",
					Value: []byte(scheduler.CorrelationKey(
						record.BKTenantID,
						dataset.Config.EventSourceID,
						raw.SourceAlertID,
					)),
				},
			}
		}
		result := producer.ProduceSync(ctx, message)
		if err := result.FirstErr(); err != nil {
			t.Fatalf("produce generated record %d (%s): %v", index, record.Scenario, err)
		}
	}
	if len(dataset.Records) != dataset.Expected.InputRecords {
		t.Fatalf("generated record count = %d, want %d", len(dataset.Records), dataset.Expected.InputRecords)
	}
}

func waitForAcceptedEvents(
	ctx context.Context,
	t *testing.T,
	process *linkdProcess,
	es *elasticsearchClient,
	index string,
	expected rawgen.Expected,
) []storedEventView {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if exited, err := process.checkExited(); exited {
			t.Fatalf("all-in-one exited while processing: %v", err)
		}
		events := loadEvents(ctx, t, es, index)
		processed := len(events) == len(expected.SourceEventIDs)
		for _, stored := range events {
			state, exists := expected.EventStates[stored.Event.SourceEventID]
			processed = processed && exists && stored.Processing.State == state
			if state == domain.EventProcessStateAccepted || state == domain.EventProcessStateSuppressed {
				processed = processed && stored.Event.RelatedAlertID != ""
			} else {
				processed = processed && stored.Event.RelatedAlertID == ""
			}
		}
		if processed {
			return events
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for accepted events: found %d/%d: %v", len(events), len(expected.SourceEventIDs), ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitForAcceptedMySQLEvents(
	ctx context.Context,
	t *testing.T,
	process *linkdProcess,
	database *sql.DB,
	expected rawgen.Expected,
) []storedEventView {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if exited, err := process.checkExited(); exited {
			t.Fatalf("all-in-one exited while processing MySQL events: %v", err)
		}
		events := loadMySQLEvents(ctx, t, database)
		processed := len(events) == len(expected.SourceEventIDs)
		for _, stored := range events {
			state, exists := expected.EventStates[stored.Event.SourceEventID]
			processed = processed && exists && stored.Processing.State == state
			if state == domain.EventProcessStateAccepted || state == domain.EventProcessStateSuppressed {
				processed = processed && stored.Event.RelatedAlertID != ""
			} else {
				processed = processed && stored.Event.RelatedAlertID == ""
			}
		}
		if processed {
			return events
		}
		select {
		case <-ctx.Done():
			t.Fatalf(
				"wait for accepted MySQL events: found %d/%d: %v",
				len(events),
				len(expected.SourceEventIDs),
				ctx.Err(),
			)
		case <-ticker.C:
		}
	}
}

func searchSources(
	ctx context.Context,
	t *testing.T,
	es *elasticsearchClient,
	index string,
) []json.RawMessage {
	t.Helper()
	body := []byte(`{"size":100,"query":{"match_all":{}},"sort":[{"_id":"asc"}]}`)
	var response struct {
		Hits struct {
			Hits []struct {
				Source json.RawMessage `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if _, err := es.do(ctx, http.MethodPost, "/"+index+"/_search", body, &response); err != nil {
		t.Fatalf("search Elasticsearch index %s: %v", index, err)
	}
	sources := make([]json.RawMessage, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		sources = append(sources, hit.Source)
	}
	return sources
}

type storedEventView struct {
	Event      domain.Event
	Processing store.EventProcessing
}

func loadEvents(ctx context.Context, t *testing.T, es *elasticsearchClient, index string) []storedEventView {
	t.Helper()
	body := []byte(`{"size":100,"query":{"match_all":{}},"sort":[{"_id":"asc"}]}`)
	var response struct {
		Hits struct {
			Hits []struct {
				Source struct {
					domain.Event
					Processing store.EventProcessing `json:"processing"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if _, err := es.do(ctx, http.MethodPost, "/"+index+"/_search", body, &response); err != nil {
		t.Fatalf("search Elasticsearch Event index %s: %v", index, err)
	}
	events := make([]storedEventView, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		events = append(events, storedEventView{Event: hit.Source.Event, Processing: hit.Source.Processing})
	}
	return events
}

func loadAlerts(ctx context.Context, t *testing.T, es *elasticsearchClient, index string) []domain.Alert {
	t.Helper()
	sources := searchSources(ctx, t, es, index)
	alerts := make([]domain.Alert, 0, len(sources))
	for _, source := range sources {
		var alert domain.Alert
		if err := json.Unmarshal(source, &alert); err != nil {
			t.Fatal(err)
		}
		alerts = append(alerts, alert)
	}
	return alerts
}

func loadAlertLogs(ctx context.Context, t *testing.T, es *elasticsearchClient, index string) []domain.AlertLog {
	t.Helper()
	sources := searchSources(ctx, t, es, index)
	logs := make([]domain.AlertLog, 0, len(sources))
	for _, source := range sources {
		var log domain.AlertLog
		if err := json.Unmarshal(source, &log); err != nil {
			t.Fatal(err)
		}
		logs = append(logs, log)
	}
	return logs
}

func loadMySQLEvents(ctx context.Context, t *testing.T, database *sql.DB) []storedEventView {
	t.Helper()
	rows, err := database.QueryContext(ctx, "SELECT payload, processing FROM linkd_events ORDER BY event_id")
	if err != nil {
		t.Fatalf("query MySQL Events: %v", err)
	}
	defer func() { _ = rows.Close() }()
	events := make([]storedEventView, 0)
	for rows.Next() {
		var payload, processingPayload []byte
		if err := rows.Scan(&payload, &processingPayload); err != nil {
			t.Fatalf("scan MySQL Event: %v", err)
		}
		var event domain.Event
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatalf("decode MySQL Event payload: %v", err)
		}
		var processing store.EventProcessing
		if err := json.Unmarshal(processingPayload, &processing); err != nil {
			t.Fatalf("decode MySQL Event processing: %v", err)
		}
		events = append(events, storedEventView{Event: event, Processing: processing})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate MySQL Events: %v", err)
	}
	return events
}

func loadMySQLAlerts(ctx context.Context, t *testing.T, database *sql.DB) []domain.Alert {
	t.Helper()
	payloads := loadMySQLPayloads(ctx, t, database, "SELECT payload FROM linkd_alerts ORDER BY alert_id")
	alerts := make([]domain.Alert, 0, len(payloads))
	for _, payload := range payloads {
		var alert domain.Alert
		if err := json.Unmarshal(payload, &alert); err != nil {
			t.Fatalf("decode MySQL Alert payload: %v", err)
		}
		alerts = append(alerts, alert)
	}
	return alerts
}

func loadMySQLAlertLogs(ctx context.Context, t *testing.T, database *sql.DB) []domain.AlertLog {
	t.Helper()
	payloads := loadMySQLPayloads(ctx, t, database, "SELECT payload FROM linkd_alert_logs ORDER BY log_id")
	logs := make([]domain.AlertLog, 0, len(payloads))
	for _, payload := range payloads {
		var log domain.AlertLog
		if err := json.Unmarshal(payload, &log); err != nil {
			t.Fatalf("decode MySQL AlertLog payload: %v", err)
		}
		logs = append(logs, log)
	}
	return logs
}

func loadMySQLPayloads(
	ctx context.Context,
	t *testing.T,
	database *sql.DB,
	query string,
) [][]byte {
	t.Helper()
	// query 只由上方固定调用点提供，不包含生成数据或外部文本。
	rows, err := database.QueryContext(ctx, query)
	if err != nil {
		t.Fatalf("query MySQL E2E payloads: %v", err)
	}
	defer func() { _ = rows.Close() }()
	payloads := make([][]byte, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scan MySQL E2E payload: %v", err)
		}
		payloads = append(payloads, append([]byte(nil), payload...))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate MySQL E2E payloads: %v", err)
	}
	return payloads
}

func assertEvents(
	t *testing.T,
	events []storedEventView,
	dataset rawgen.Dataset,
) {
	t.Helper()
	expected := dataset.Expected
	rawEvents := generatedStandardRecords(t, dataset)
	wantIDs := append([]string(nil), expected.SourceEventIDs...)
	sort.Strings(wantIDs)
	gotIDs := make([]string, 0, len(events))
	for _, stored := range events {
		event := stored.Event
		gotIDs = append(gotIDs, event.SourceEventID)
		rawRecord, exists := rawEvents[event.SourceEventID]
		if !exists {
			t.Fatalf("Event references an unknown generated source event: %#v", event)
		}
		raw := rawRecord.Raw
		wantState := expected.EventStates[event.SourceEventID]
		if stored.Processing.State != wantState {
			t.Fatalf("Event processing state = %q, want %q: %#v", stored.Processing.State, wantState, event)
		}
		if (wantState == domain.EventProcessStateAccepted || wantState == domain.EventProcessStateSuppressed) &&
			event.RelatedAlertID == "" {
			t.Fatalf("associated Event has no related_alert_id: %#v", event)
		}
		if wantState != domain.EventProcessStateAccepted && wantState != domain.EventProcessStateSuppressed &&
			event.RelatedAlertID != "" {
			t.Fatalf("unassociated Event has related_alert_id: %#v", event)
		}
		if event.BKTenantID != rawRecord.BKTenantID || event.EventSourceID != dataset.Config.EventSourceID ||
			event.Fingerprint != raw.SourceAlertID || event.Severity != raw.SourceSeverity ||
			event.Title != raw.Title || event.Content != raw.Content || event.Action != raw.Action ||
			!reflect.DeepEqual(event.Dimensions, raw.Dimensions) {
			t.Fatalf("standard basic field mapping mismatch: event=%#v raw=%#v", event, raw)
		}
		if !event.OccurredAt.Equal(raw.OccurredAt) || !event.ReceivedAt.Equal(rawRecord.KafkaTimestamp) {
			t.Fatalf("standard time mapping mismatch: event=%#v raw=%#v kafka_time=%s", event, raw, rawRecord.KafkaTimestamp)
		}
		if event.SubjectSystem != raw.SubjectSystem || event.SubjectType != raw.SubjectType ||
			event.SubjectID != raw.SubjectID || event.SubjectName != raw.SubjectName {
			t.Fatalf("standard subject mapping mismatch: event=%#v raw=%#v", event, raw)
		}
		if event.SourceAlertID != raw.SourceAlertID || event.SourceEventID != raw.SourceEventID {
			t.Fatalf("standard source field mapping mismatch: event=%#v raw=%#v", event, raw)
		}
		parsedID, err := domain.ParseEventID(event.EventID)
		if err != nil || parsedID.BKTenantID != event.BKTenantID ||
			parsedID.EventSourceID != event.EventSourceID ||
			!parsedID.Timestamp.Equal(event.ReceivedAt.Truncate(time.Second)) {
			t.Fatalf("Event ID %q is not a valid deterministic route: %#v, %v", event.EventID, parsedID, err)
		}
	}
	sort.Strings(gotIDs)
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("source event IDs = %v, want %v", gotIDs, wantIDs)
	}
}

type generatedStandardRecord struct {
	Raw            cleaner.EventDraft
	BKTenantID     string
	KafkaTimestamp time.Time
}

func generatedStandardRecords(t *testing.T, dataset rawgen.Dataset) map[string]generatedStandardRecord {
	t.Helper()
	events := make(map[string]generatedStandardRecord, len(dataset.Expected.SourceEventIDs))
	for _, record := range dataset.Records {
		if !record.Valid {
			continue
		}
		raw, err := (cleaner.StandardCleaner{}).Clean(context.Background(), cleaner.RawEventMessage{Payload: record.Body})
		if err != nil {
			t.Fatalf("decode generated valid standard event: %v", err)
		}
		if existing, duplicate := events[raw.SourceEventID]; duplicate {
			if existing.BKTenantID != record.BKTenantID || !reflect.DeepEqual(existing.Raw, raw) ||
				!existing.KafkaTimestamp.Equal(record.KafkaTimestamp) {
				t.Fatalf("duplicate source event %q changed its identity payload", raw.SourceEventID)
			}
			continue
		}
		events[raw.SourceEventID] = generatedStandardRecord{
			Raw: raw, BKTenantID: record.BKTenantID, KafkaTimestamp: record.KafkaTimestamp,
		}
	}
	return events
}

func assertAlerts(t *testing.T, alerts []domain.Alert, expected []rawgen.ExpectedAlert) {
	t.Helper()
	wantTotal := 0
	for _, item := range expected {
		wantTotal += item.Count
		count := 0
		for _, alert := range alerts {
			if alert.BKTenantID == item.BKTenantID && alert.Fingerprint == item.Fingerprint &&
				alert.Severity == item.Severity && alert.Status == item.Status {
				count++
			}
		}
		if count != item.Count {
			t.Fatalf("alert expectation %#v matched %d alerts", item, count)
		}
	}
	if len(alerts) != wantTotal {
		t.Fatalf("alert count = %d, want %d", len(alerts), wantTotal)
	}
}

func assertAlertLogs(t *testing.T, logs []domain.AlertLog, expected map[domain.OperationKind]int) {
	t.Helper()
	counts := make(map[domain.OperationKind]int)
	for _, log := range logs {
		counts[log.OperationKind]++
	}
	wantTotal := 0
	for operation, want := range expected {
		wantTotal += want
		if counts[operation] != want {
			t.Fatalf("AlertLog operation %q count = %d, want %d; all=%v", operation, counts[operation], want, counts)
		}
	}
	if len(logs) != wantTotal || len(counts) != len(expected) {
		t.Fatalf("AlertLog total/count kinds = %d/%d, want %d/%d; all=%v", len(logs), len(counts), wantTotal, len(expected), counts)
	}
}

func consumeOutputs(
	ctx context.Context,
	t *testing.T,
	broker string,
	names resourceNames,
	expected int,
) []kafkahook.Message {
	t.Helper()
	consumer, err := kgo.NewClient(
		kgo.SeedBrokers(broker),
		kgo.ClientID("linkd-e2e-output-reader-"+names.Token),
		kgo.ConsumerGroup(names.OutputGroup),
		kgo.ConsumeTopics(names.OutputTopic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	messages := make([]kafkahook.Message, 0, expected)
	for len(messages) < expected {
		fetches := consumer.PollRecords(ctx, expected-len(messages))
		if err := fetches.Err(); err != nil {
			t.Fatalf("consume output Kafka: %v", err)
		}
		for _, record := range fetches.Records() {
			var message kafkahook.Message
			if err := json.Unmarshal(record.Value, &message); err != nil {
				t.Fatalf("decode output Kafka message: %v", err)
			}
			headers := make(map[string]string, len(record.Headers))
			for _, header := range record.Headers {
				headers[header.Key] = string(header.Value)
			}
			if headers["message_id"] != message.MessageID || headers["cause_id"] != message.Cause.ID ||
				headers["bk_tenant_id"] != message.BKTenantID {
				t.Fatalf("output Kafka header/payload mismatch: headers=%v message=%#v", headers, message)
			}
			messages = append(messages, message)
		}
	}
	quietCtx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	extra := consumer.PollRecords(quietCtx, 1)
	if records := extra.Records(); len(records) != 0 {
		t.Fatalf("output Kafka contains unexpected duplicate message: %s", records[0].Value)
	}
	return messages
}

func assertOutputs(
	t *testing.T,
	outputs []kafkahook.Message,
	events []storedEventView,
	expected int,
) {
	t.Helper()
	eventIDs := make(map[string]struct{}, len(events))
	suppressedEventIDs := make(map[string]struct{})
	for _, stored := range events {
		eventIDs[stored.Event.EventID] = struct{}{}
		if stored.Processing.State == domain.EventProcessStateSuppressed {
			suppressedEventIDs[stored.Event.EventID] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(outputs))
	for _, output := range outputs {
		if _, exists := eventIDs[output.Cause.ID]; !exists {
			t.Fatalf("output references unknown Event %q", output.Cause.ID)
		}
		if _, suppressed := suppressedEventIDs[output.Cause.ID]; suppressed {
			t.Fatalf("suppressed Event %q unexpectedly emitted an Alert snapshot", output.Cause.ID)
		}
		if _, duplicate := seen[output.MessageID]; duplicate {
			t.Fatalf("output repeated message %q", output.MessageID)
		}
		seen[output.MessageID] = struct{}{}
		if output.SchemaVersion != "1" || output.Alert.AlertID != output.AlertID ||
			output.EnrichStatus != domain.EnrichStatusSucceeded {
			t.Fatalf("invalid output message: %#v", output)
		}
	}
	if len(outputs) != expected || len(seen) != expected {
		t.Fatalf("output messages = %d unique=%d, want %d", len(outputs), len(seen), expected)
	}
}

func waitForRedisDrain(
	ctx context.Context,
	t *testing.T,
	client *redis.Client,
	names resourceNames,
) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		groups, groupsErr := client.XInfoGroups(ctx, names.SignalStream).Result()
		mailboxesDrained, mailboxErr := redisMailboxesDrained(ctx, client, names.LockPrefix+":mailbox")
		if groupsErr == nil && mailboxErr == nil && mailboxesDrained && len(groups) == 1 &&
			groups[0].Name == names.SignalGroup && groups[0].Pending == 0 && groups[0].Lag == 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for Redis mailbox drain: groups=%v mailboxes_drained=%v errors=%v/%v",
				groups, mailboxesDrained, groupsErr, mailboxErr)
		case <-ticker.C:
		}
	}
}

func redisMailboxesDrained(ctx context.Context, client *redis.Client, keyPrefix string) (bool, error) {
	var cursor uint64
	for {
		keys, next, err := client.Scan(ctx, cursor, keyPrefix+":*:events", 100).Result()
		if err != nil {
			return false, err
		}
		for _, key := range keys {
			length, err := client.LLen(ctx, key).Result()
			if err != nil {
				return false, err
			}
			if length > 0 {
				return false, nil
			}
		}
		cursor = next
		if cursor == 0 {
			return true, nil
		}
	}
}
