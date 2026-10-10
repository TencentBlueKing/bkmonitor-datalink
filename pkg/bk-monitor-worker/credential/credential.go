// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License. See the License at http://opensource.org/licenses/MIT.

// Package credential loads one deployment credential snapshot before clients start.
package credential

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"reflect"
	"strings"

	"github.com/TencentBlueKing/bk-kms-sdk/go/decrypt"
	consul "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/consul"
	"github.com/hashicorp/consul/api"
	"github.com/spf13/viper"
)

type account struct {
	Username *string `json:"username"`
	Password *string `json:"password"`
}
type app struct {
	Code   *string `json:"app_code"`
	Secret *string `json:"app_secret"`
}
type password struct {
	Password *string `json:"password"`
}
type encryption struct {
	AESKey        *string `json:"aes_key"`
	BkdataAESKey  *string `json:"bkdata_aes_key"`
	BkdataAESIV   *string `json:"bkdata_aes_iv"`
	BkdataToken   *string `json:"bkdata_token"`
	APMHashSecret *string `json:"apm_hash_secret"`
}
type reportTokens struct {
	LogSearch *string `json:"log_search"`
	RabbitMQ  *string `json:"rabbitmq"`
	SLO       *string `json:"slo"`
	Profile   *string `json:"profile"`
}
type prometheus struct {
	Headers *stringMap `json:"headers"`
}

// JSON null is not a string header value; encoding/json otherwise coerces it
// to the empty string when unmarshalling directly into map[string]string.
type stringMap map[string]string

func (m *stringMap) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	result := stringMap{}
	for key, value := range fields {
		var text *string
		if err := json.Unmarshal(value, &text); err != nil || text == nil {
			return fmt.Errorf("invalid string map")
		}
		result[key] = *text
	}
	*m = result
	return nil
}

type consulAuth struct {
	Token         *string `json:"token"`
	Username      *string `json:"username"`
	Password      *string `json:"password"`
	TLSPrivateKey *string `json:"tls_private_key_base64"`
}
type payload struct {
	Version    int                   `json:"schema_version"`
	Service    string                `json:"service"`
	Apps       map[string]app        `json:"bkapp_id_secret"`
	MySQL      map[string]account    `json:"mysql"`
	Redis      map[string]password   `json:"redis"`
	Sentinel   map[string]password   `json:"redis_sentinel"`
	Encryption encryption            `json:"encryption"`
	Reports    reportTokens          `json:"report_tokens"`
	Prometheus prometheus            `json:"prometheus"`
	Consul     map[string]consulAuth `json:"consul"`
	RabbitMQ   map[string]account    `json:"rabbitmq"`
}

// Snapshot contains only an already validated set of overrides. No file watcher
// or reload path rereads deployment credentials during the process lifetime.
type Snapshot struct {
	values        map[string]any
	ConsulOptions *consul.ClientOptions
}

func (s *Snapshot) Apply(v *viper.Viper) {
	for key, value := range s.values {
		v.Set(key, value)
	}
}

var sensitiveKeys = []string{
	"store.mysql.user", "store.mysql.password",
	"broker.redis.standalone.password", "broker.redis.sentinel.password",
	"store.redis.standalone.password", "store.redis.sentinel.password",
	"store.dependentRedis.standalone.password", "store.dependentRedis.sentinel.password",
	"taskConfig.common.bkapi.appSecret", "aes.key", "aes.bkdataAESKey", "aes.bkdataAESIv", "aes.bkdataToken",
	"taskConfig.apmPreCalculate.hashSecret", "taskConfig.logSearch.metric.reportAccessToken",
	"taskConfig.rabbitmqMetric.reportAccessToken", "taskConfig.metadata.slo.sloPushGatewayToken",
	"taskConfig.apmPreCalculate.metrics.profile.token", "taskConfig.apmPreCalculate.metrics.report.prometheus.headers",
	"store.consul.token", "store.consul.token_file", "store.consul.username", "store.consul.password", "store.consul.tls.key_file",
}

func envName(prefix, key string) string {
	return strings.ToUpper(prefix + "_" + strings.ReplaceAll(key, ".", "_"))
}

// Prepare reads the reserved control segment from raw, which has no env binding.
// All validation completes before any active Viper value is overwritten.
func Prepare(v, raw *viper.Viper, prefix string) (*Snapshot, error) {
	for _, key := range []string{"kms.enabled", "kms.envelope_file", "kms.private_key_file"} {
		if _, found := os.LookupEnv(envName(prefix, key)); found {
			return nil, fmt.Errorf("kms control conflict: %s", key)
		}
	}
	if !raw.IsSet("kms.enabled") {
		return nil, nil
	}
	enabled, ok := raw.Get("kms.enabled").(bool)
	if !ok {
		return nil, fmt.Errorf("kms control: invalid kms.enabled")
	}
	if !enabled {
		return nil, nil
	}
	for _, key := range raw.AllKeys() {
		if strings.HasPrefix(key, "kms.") && key != "kms.enabled" && key != "kms.envelope_file" && key != "kms.private_key_file" {
			return nil, fmt.Errorf("kms control: unsupported field")
		}
	}
	for _, key := range sensitiveKeys {
		if hasValue(raw.Get(key)) {
			return nil, fmt.Errorf("kms plaintext conflict: %s", key)
		}
		if os.Getenv(envName(prefix, key)) != "" {
			return nil, fmt.Errorf("kms environment conflict: %s", key)
		}
	}
	for _, key := range []string{"CONSUL_HTTP_TOKEN", "CONSUL_HTTP_TOKEN_FILE", "CONSUL_HTTP_AUTH", "CONSUL_CLIENT_KEY"} {
		if os.Getenv(key) != "" {
			return nil, fmt.Errorf("kms environment conflict: %s", key)
		}
	}
	if os.Getenv(envName(prefix, "taskConfig.rabbitmqMetric.instances")) != "" {
		return nil, fmt.Errorf("kms environment conflict: taskConfig.rabbitmqMetric.instances")
	}
	if err := validateAddresses(v); err != nil {
		return nil, err
	}
	envelope, err := readFile(raw, "kms.envelope_file")
	if err != nil {
		return nil, err
	}
	privateKey, err := readFile(raw, "kms.private_key_file")
	if err != nil {
		return nil, err
	}
	plaintext, err := decryptEnvelope(envelope, privateKey)
	if err != nil {
		return nil, err
	}
	return preparePayload(v, plaintext)
}

func readFile(v *viper.Viper, key string) (string, error) {
	path, ok := v.Get(key).(string)
	if !ok || path == "" {
		return "", fmt.Errorf("kms control: missing %s", key)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("kms file: cannot read %s", key)
	}
	return strings.TrimSpace(string(data)), nil
}

func decryptEnvelope(envelope, privateKey string) (plaintext string, err error) {
	// The SDK's failure details may include malformed envelope fields. Never
	// propagate them to startup diagnostics, including a malformed-data panic.
	defer func() {
		if recover() != nil {
			plaintext = ""
			err = fmt.Errorf("kms decrypt failed")
		}
	}()
	plaintext, err = decrypt.Decrypt(envelope, privateKey)
	if err != nil {
		return "", fmt.Errorf("kms decrypt failed")
	}
	return plaintext, nil
}

func hasValue(value any) bool {
	if value == nil {
		return false
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.String:
		return rv.Len() != 0
	case reflect.Map:
		for _, key := range rv.MapKeys() {
			if hasValue(rv.MapIndex(key).Interface()) {
				return true
			}
		}
		return false
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			if hasValue(rv.Index(i).Interface()) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func preparePayload(v *viper.Viper, plaintext string) (*Snapshot, error) {
	var p payload
	decoder := json.NewDecoder(strings.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil {
		return nil, fmt.Errorf("kms payload: invalid schema")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("kms payload: invalid JSON document")
	}
	if p.Version != 1 || p.Service != "bk-monitor-worker" {
		return nil, fmt.Errorf("kms payload: unsupported schema_version or service")
	}
	for _, aliases := range []map[string]bool{
		allowedAliases(p.Apps, "default"), allowedAliases(p.MySQL, "default"), allowedAliases(p.Consul, "default"),
		allowedAliases(p.Redis, "broker", "store", "dependent"), allowedAliases(p.Sentinel, "broker", "store", "dependent"),
	} {
		if aliases == nil {
			return nil, fmt.Errorf("kms payload: unsupported credential alias")
		}
	}
	s := &Snapshot{values: map[string]any{}}
	bind := func(key string, value *string, required bool) error {
		if value == nil {
			if required {
				return fmt.Errorf("kms payload: missing %s", key)
			}
			return nil
		}
		s.values[key] = *value
		return nil
	}
	for alias, key := range map[string]string{"broker": "broker.redis", "store": "store.redis", "dependent": "store.dependentRedis"} {
		if err := bind(key+".standalone.password", p.Redis[alias].Password, true); err != nil {
			return nil, err
		}
		if err := bind(key+".sentinel.password", p.Sentinel[alias].Password, v.GetString(key+".mode") == "sentinel"); err != nil {
			return nil, err
		}
	}
	my := p.MySQL["default"]
	a := p.Apps["default"]
	for _, item := range []struct {
		key      string
		value    *string
		required bool
	}{
		{"store.mysql.user", my.Username, true}, {"store.mysql.password", my.Password, true},
		// API consumers use this identity regardless of the legacy enabled key.
		{"taskConfig.common.bkapi.appCode", a.Code, true},
		{"taskConfig.common.bkapi.appSecret", a.Secret, true},
		{"aes.key", p.Encryption.AESKey, true}, {"aes.bkdataAESKey", p.Encryption.BkdataAESKey, true},
		{"aes.bkdataAESIv", p.Encryption.BkdataAESIV, true}, {"aes.bkdataToken", p.Encryption.BkdataToken, true},
		{"taskConfig.apmPreCalculate.hashSecret", p.Encryption.APMHashSecret, true},
		{"taskConfig.logSearch.metric.reportAccessToken", p.Reports.LogSearch, v.GetString("taskConfig.logSearch.metric.reportUrl") != ""},
		{"taskConfig.metadata.slo.sloPushGatewayToken", p.Reports.SLO, v.GetString("taskConfig.metadata.slo.sloPushGatewayEndpoint") != ""},
		{"taskConfig.apmPreCalculate.metrics.profile.token", p.Reports.Profile, v.GetBool("taskConfig.apmPreCalculate.metrics.profile.enabled")},
	} {
		if err := bind(item.key, item.value, item.required); err != nil {
			return nil, err
		}
	}
	if (a.Code == nil) != (a.Secret == nil) {
		return nil, fmt.Errorf("kms payload: app identity must be paired")
	}
	if a.Code != nil && v.IsSet("taskConfig.common.bkapi.appCode") && v.GetString("taskConfig.common.bkapi.appCode") != *a.Code {
		return nil, fmt.Errorf("kms identity conflict: taskConfig.common.bkapi.appCode")
	}
	if p.Prometheus.Headers != nil {
		s.values["taskConfig.apmPreCalculate.metrics.report.prometheus.headers"] = map[string]string(*p.Prometheus.Headers)
	} else if v.GetString("taskConfig.apmPreCalculate.metrics.report.prometheus.url") != "" {
		return nil, fmt.Errorf("kms payload: missing prometheus.headers")
	}
	if err := s.bindRabbitMQ(v, p.RabbitMQ, p.Reports.RabbitMQ); err != nil {
		return nil, err
	}
	opts, err := prepareConsul(v, p.Consul["default"])
	if err != nil {
		return nil, err
	}
	s.ConsulOptions = opts
	return s, nil
}

func allowedAliases[T any](values map[string]T, allowed ...string) map[string]bool {
	result := map[string]bool{}
	for _, key := range allowed {
		result[key] = true
	}
	for key := range values {
		if !result[key] {
			return nil
		}
	}
	return result
}

func (s *Snapshot) bindRabbitMQ(v *viper.Viper, accounts map[string]account, token *string) error {
	const key = "taskConfig.rabbitmqMetric.instances"
	var instances []map[string]any
	if v.UnmarshalKey(key, &instances) != nil {
		return fmt.Errorf("kms rabbitmq: invalid instances")
	}
	enabled := !v.IsSet("taskConfig.rabbitmqMetric.enabled") || v.GetBool("taskConfig.rabbitmqMetric.enabled")
	for i, instance := range instances {
		if hasValue(instance["username"]) || hasValue(instance["password"]) {
			return fmt.Errorf("kms plaintext conflict: %s[%d]", key, i)
		}
		if !enabled {
			continue
		}
		ref, _ := instance["credentialsref"].(string)
		if ref == "" {
			return fmt.Errorf("kms rabbitmq: missing credentialsRef at instance %d", i)
		}
		cred, ok := accounts[ref]
		if !ok || cred.Username == nil || cred.Password == nil {
			return fmt.Errorf("kms rabbitmq: invalid credentialsRef at instance %d", i)
		}
		instance["username"], instance["password"] = *cred.Username, *cred.Password
	}
	if enabled && len(instances) > 0 && token == nil {
		return fmt.Errorf("kms payload: missing report_tokens.rabbitmq")
	}
	if token != nil {
		s.values["taskConfig.rabbitmqMetric.reportAccessToken"] = *token
	}
	if v.IsSet(key) {
		s.values[key] = instances
	}
	return nil
}

func prepareConsul(v *viper.Viper, auth consulAuth) (*consul.ClientOptions, error) {
	if auth.Token == nil && auth.Username == nil && auth.TLSPrivateKey == nil {
		return nil, fmt.Errorf("kms payload: missing consul.default authentication")
	}
	if (auth.Username == nil) != (auth.Password == nil) {
		return nil, fmt.Errorf("kms payload: consul BasicAuth must be paired")
	}
	opts := &consul.ClientOptions{TLSConfig: api.DefaultConfig().TLSConfig}
	if auth.Token != nil {
		opts.Token = *auth.Token
	}
	if auth.Username != nil {
		opts.Username, opts.Password = *auth.Username, *auth.Password
	}
	for key, target := range map[string]*string{
		"store.consul.tls.ca_file": &opts.TLSConfig.CAFile, "store.consul.tls.cert_file": &opts.TLSConfig.CertFile,
		"store.consul.tls.server_name": &opts.TLSConfig.Address,
	} {
		if v.IsSet(key) {
			*target = v.GetString(key)
		}
	}
	if v.IsSet("store.consul.tls.insecure_skip_verify") {
		opts.TLSConfig.InsecureSkipVerify = v.GetBool("store.consul.tls.insecure_skip_verify")
	}
	if auth.TLSPrivateKey != nil {
		key, err := base64.StdEncoding.DecodeString(*auth.TLSPrivateKey)
		if err != nil {
			return nil, fmt.Errorf("kms payload: invalid consul TLS private key")
		}
		cert, err := os.ReadFile(opts.TLSConfig.CertFile)
		if err != nil {
			return nil, fmt.Errorf("kms consul: cannot read public certificate")
		}
		opts.TLSConfig.KeyPEM, opts.TLSConfig.CertPEM = key, cert
		opts.TLSConfig.KeyFile, opts.TLSConfig.CertFile = "", ""
		if _, err := api.NewHttpClient(api.DefaultConfig().Transport, opts.TLSConfig); err != nil {
			return nil, fmt.Errorf("kms consul: invalid TLS material")
		}
	} else if opts.TLSConfig.CertFile != "" {
		return nil, fmt.Errorf("kms payload: missing consul TLS private key")
	}
	return opts, nil
}

func validateAddresses(v *viper.Viper) error {
	for _, key := range []string{
		"store.mysql.host", "broker.redis.standalone.host", "store.redis.standalone.host", "store.dependentRedis.standalone.host",
		"store.consul.address", "store.consul.addr", "taskConfig.common.bkapi.host", "taskConfig.common.bkapi.bkdataApiBaseUrl",
		"taskConfig.common.bkapi.bkgseApiGwUrl", "taskConfig.common.bkapi.cmdbApiGatewayUrl", "taskConfig.common.bkapi.bkmonitorApiGatewayBaseUrl",
		"taskConfig.logSearch.metric.reportUrl", "taskConfig.rabbitmqMetric.reportUrl", "taskConfig.metadata.slo.sloPushGatewayEndpoint",
		"taskConfig.apmPreCalculate.metrics.profile.host", "taskConfig.apmPreCalculate.metrics.report.prometheus.url",
	} {
		if err := validateAddress(v.GetString(key)); err != nil {
			return fmt.Errorf("kms address credentials conflict: %s", key)
		}
	}
	for _, key := range []string{"broker.redis.sentinel.address", "store.redis.sentinel.address", "store.dependentRedis.sentinel.address"} {
		for _, address := range v.GetStringSlice(key) {
			if validateAddress(address) != nil {
				return fmt.Errorf("kms address credentials conflict: %s", key)
			}
		}
	}
	var instances []map[string]any
	if v.UnmarshalKey("taskConfig.rabbitmqMetric.instances", &instances) != nil {
		return fmt.Errorf("kms rabbitmq: invalid instances")
	}
	for i, instance := range instances {
		if address, ok := instance["domainname"].(string); ok && validateAddress(address) != nil {
			return fmt.Errorf("kms address credentials conflict: rabbitmq instance %d", i)
		}
	}
	return nil
}

func validateAddress(address string) error {
	if address == "" {
		return nil
	}
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	u, err := url.Parse(address)
	if err != nil || u.User != nil {
		return fmt.Errorf("invalid address")
	}
	for key := range u.Query() {
		switch strings.ToLower(key) {
		case "token", "access_token", "accesstoken", "password", "secret", "app_secret", "appsecret", "authorization", "auth", "key", "api_key", "apikey", "api-key":
			return fmt.Errorf("authentication query")
		}
	}
	return nil
}
