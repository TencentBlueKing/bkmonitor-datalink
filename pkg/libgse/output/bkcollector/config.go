package bkcollector

type Config struct {
	GrpcHost       string `config:"otlp_grpc_host"`
	BkDataToken    string `config:"otlp_bk_data_token"`
	EventBufferMax int32  `config:"eventbuffermax"`
}

var defaultConfig = Config{}
