package config

type TlsConfig struct {
	SkipVerify bool   `yaml:"skip_verify"`
	CAFile     string `yaml:"ca_file"`
	KeyFile    string `yaml:"key_file"`
	CertFile   string `yaml:"cert_file"`
}
