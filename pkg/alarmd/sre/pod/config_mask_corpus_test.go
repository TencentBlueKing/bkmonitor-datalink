// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"strconv"
	"strings"
	"testing"
)

// credentialCorpus is the reference list of credential shapes the ConfigMap
// view masks (config_values_blacklist_v1), drawn from the configuration of
// software commonly run beside alarmd: connection URLs and DSNs, Redis,
// Kafka, MySQL, PostgreSQL, web servers, cloud SDKs. Each entry is the text
// as it is written and the parts of it that must never be shown.
var credentialCorpus = []struct {
	name    string
	text    string
	secrets []string
}{
	// URL userinfo, any scheme, the user possibly empty.
	{"mongodb url", "mongodb://admin:M0ngoPw@mongo.example.test:27017/app", []string{"M0ngoPw"}},
	{"amqp url", "amqp://guest:guestpw@rabbit.example.test:5672/", []string{"guestpw"}},
	{"redis url empty user", "redis://:R3disPass@redis.example.test:6379/0", []string{"R3disPass"}},
	{"postgres url", "postgres://app:PgPw-42@192.0.2.10:5432/app?sslmode=disable", []string{"PgPw-42"}},
	{"mysql url", "mysql://root:MyPw_9@192.0.2.11:3306/db", []string{"MyPw_9"}},
	{"http basic url", "http://ops:basicpw@proxy.example.test:3128", []string{"basicpw"}},
	// Credentials in a query.
	{"query access_token", "https://api.example.test/v1?access_token=at-77x&limit=10", []string{"at-77x"}},
	{"query token", "https://api.example.test/hook?token=tk_55", []string{"tk_55"}},
	{"query password", "https://db.example.test/?user=u&password=qp-pw-1", []string{"qp-pw-1"}},
	// Go and JDBC DSNs.
	{"go dsn tcp", "root:Pa55word!@tcp(mysql.example.test:3306)/db?parseTime=true", []string{"Pa55word!"}},
	{"go dsn unix", "app:dsnpw@unix(/var/run/mysqld/mysqld.sock)/db", []string{"dsnpw"}},
	{"jdbc query", "jdbc:mysql://192.0.2.12:3306/db?user=root&password=JdbcPw1", []string{"JdbcPw1"}},
	{"jdbc properties", "jdbc:sqlserver://db.example.test:1433;databaseName=app;user=sa;password=SqlPw2;", []string{"SqlPw2"}},
	// Redis.
	{"requirepass", "requirepass R3disPass", []string{"R3disPass"}},
	{"masterauth", "masterauth MasterPw7", []string{"MasterPw7"}},
	// Credential names, any case and separator.
	{"pass in json", `{"pass":"El4sticPw","host":"es.example.test"}`, []string{"El4sticPw"}},
	{"password upper", "PASSWORD: Upper-Pw", []string{"Upper-Pw"}},
	{"pass with spaces", "pass: two words here", []string{"words here"}},
	{"passwd", "db_passwd=passwdval", []string{"passwdval"}},
	{"camel password", "redisPassword: camelpw", []string{"camelpw"}},
	{"client secret", "clientSecret: cs-value-8", []string{"cs-value-8"}},
	{"api_key", "api_key: ak-value-9", []string{"ak-value-9"}},
	{"apiKey", "apiKey: ak-value-10", []string{"ak-value-10"}},
	{"access key", "ACCESS_KEY=ak-value-11", []string{"ak-value-11"}},
	{"secret key", "secret_key: sk-value-12", []string{"sk-value-12"}},
	{"credential", "credential: cred-value-13", []string{"cred-value-13"}},
	{"token", "token: tok-value-14", []string{"tok-value-14"}},
	{"auth token", "x_auth_token: tok-value-15", []string{"tok-value-15"}},
	// JAAS.
	{"sasl.jaas.config", `sasl.jaas.config=org.apache.kafka.common.security.plain.PlainLoginModule required username="alice" password="K4fkaPw";`, []string{"K4fkaPw"}},
	{"jaas file", `KafkaClient { org.apache.kafka.common.security.scram.ScramLoginModule required username="bob" password="Scr4mPw"; };`, []string{"Scr4mPw"}},
	// my.cnf, .pgpass, htpasswd.
	{"my.cnf", "password = MyCnfPw", []string{"MyCnfPw"}},
	{"my.cnf quoted", `password="quoted pw"`, []string{"quoted pw"}},
	{"pgpass", "db.example.test:5432:app:app:PgPassPw", []string{"PgPassPw"}},
	{"htpasswd apr1", "admin:$apr1$abcdefgh$0123456789abcdefghijkl", []string{"0123456789abcdefghijkl"}},
	{"htpasswd bcrypt", "ops:$2y$05$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ012", []string{"ABCDEFGHIJKLMNOPQRSTUVWXYZ012"}},
	{"htpasswd sha", "web:{SHA}W6ph5Mm5Pz8GgiULbPgzG37mj9g=", []string{"W6ph5Mm5Pz8GgiULbPgzG37mj9g="}},
	// Authorization.
	{"authorization basic", "Authorization: Basic dXNlcjpwYXNz", []string{"dXNlcjpwYXNz"}},
	{"authorization bearer", "Authorization: Bearer tok.part-two.part3", []string{"tok.part-two.part3"}},
	// AWS-style key pair.
	{"aws key pair", "aws_access_key_id = AKIA0123456789ABCDEF\naws_secret_access_key = wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLEKEY", []string{"AKIA0123456789ABCDEF", "wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLEKEY"}},
	// PEM private key.
	{"pem private key", "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC\nAB==\n-----END PRIVATE KEY-----", []string{"MIIEvQIBADANBgkqhkiG9w0BAQEFAASC", "PRIVATE KEY"}},
}

// plainCorpus is configuration that must come back exactly as written: a
// view that masks it costs the agent the file it came to read.
var plainCorpus = []struct{ name, text string }{
	{"corefile", ".:53 {\n    errors\n    health\n    kubernetes cluster.local in-addr.arpa ip6.arpa {\n       pods insecure\n       fallthrough in-addr.arpa ip6.arpa\n    }\n    forward . /etc/resolv.conf\n    cache 30\n    reload\n}"},
	{"nodehosts", "192.0.2.20 node-a\n192.0.2.21 node-b"},
	{"path", "/etc/ssl/certs/ca-bundle.pem"},
	{"key file path", "key_file: /etc/alarmd/secret/admin_key"},
	{"password file path", "password_file: /etc/alarmd/secret/redis_password"},
	{"url without userinfo", "https://mirror.example.test/repo?ref=main"},
	{"redis url without userinfo", "redis://redis.example.test:6379/0"},
	{"mongodb url without userinfo", "mongodb://mongo.example.test:27017"},
	{"password_min_length", "password_min_length: 8"},
	{"token_ttl", "token_ttl: 3600"},
	{"token_expire_seconds", "token_expire_seconds=600"},
	{"password_policy", "password_policy: strict"},
	{"secretRef", "secretRef: my-secret"},
	{"cache key", "cache_key: user-profile"},
	{"routing key", "routing_key: alerts"},
	{"cert in prose", "# set the cert path below"},
	{"username", "username: alice"},
	{"auth switch", "auth: true"},
	{"secure_cookie switch", "secure_cookie: true"},
	{"cookie_http_only switch", "cookie_http_only: false"},
	{"use_token switch", "use_token: on"},
	{"nginx proxy_pass", "location / {\n    proxy_pass http://backend.example.test:8080;\n    fastcgi_pass 192.0.2.30:9000;\n}"},
}

// TestTheCredentialCorpusIsMaskedWhereverItStands holds every shape of the
// corpus masked as a whole value, as a line among plain lines and as the
// value of a plain key in a YAML document; the plain line beside it is kept.
// A masked part shows only its size and fingerprint.
func TestTheCredentialCorpusIsMaskedWhereverItStands(t *testing.T) {
	for _, entry := range credentialCorpus {
		contexts := map[string]string{
			"whole value": entry.text,
			"among lines": "host: a.example.test\n" + entry.text + "\nport: 8080",
		}
		if !strings.Contains(entry.text, "\n") {
			contexts["yaml value"] = "host: a.example.test\nconn: " + strconv.Quote(entry.text) + "\nport: 8080\n"
		}
		for context, text := range contexts {
			t.Run(entry.name+"/"+context, func(t *testing.T) {
				shown := shownValue(t, "notes", text)
				for _, secret := range entry.secrets {
					if strings.Contains(shown, secret) {
						t.Fatalf("%q shown:\n%s", secret, shown)
					}
				}
				if !strings.Contains(shown, "<redacted bytes=") {
					t.Fatalf("nothing masked:\n%s", shown)
				}
				if context != "whole value" && (!strings.Contains(shown, "a.example.test") || !strings.Contains(shown, "8080")) {
					t.Fatalf("the plain lines around it were lost:\n%s", shown)
				}
			})
		}
	}
}

// TestThePlainCorpusIsShownAsWritten holds every plain entry shown exactly,
// alone and beside other configuration.
func TestThePlainCorpusIsShownAsWritten(t *testing.T) {
	for _, entry := range plainCorpus {
		t.Run(entry.name, func(t *testing.T) {
			if shown := shownValue(t, "notes", entry.text); shown != entry.text {
				t.Fatalf("shown as\n%s\nwant\n%s", shown, entry.text)
			}
		})
	}
}
