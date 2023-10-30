package bkcollector

import (
	"github.com/elastic/beats/libbeat/logp"
	"net"
	"net/url"
)

func BkCollectorConnect(grpcHost string) error {
	address, Error := url.Parse(grpcHost)
	if Error != nil {
		logp.Err("failed to parse URL: %v", Error)
		return Error
	}
	conn, err := net.Dial("tcp", address.Host)
	if err != nil {
		logp.Err("bkcollector 服务无法连接, %v", err)
		defer conn.Close()
		return err
	}
	return nil
}
