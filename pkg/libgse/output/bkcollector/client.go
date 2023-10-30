package bkcollector

import (
	"fmt"
	"net"
	"net/url"

	"github.com/elastic/beats/libbeat/logp"
)

func BkCollectorConnect(ip string, port string) error {
	address := net.JoinHostPort(ip, fmt.Sprint(port))
	conn, err := net.Dial("tcp", address)
	if err != nil {
		logp.Err("bkcollector 服务无法连接, %v", err)
		defer conn.Close()
		return err
	}
	return nil
}

func GetIpPort(host string) (string, string, error) {

	// 解析 URL
	parsedURL, err := url.Parse(host)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse URL: %v", err)
	}

	// 获取 IP 地址和端口号
	ip := parsedURL.Hostname()
	port := parsedURL.Port()
	return ip, port, nil
}
