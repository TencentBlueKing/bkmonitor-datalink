package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	queryHTTP "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http"
	tsdbService "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

// 此程序可在已发布版本的模块目录构建，测试通过真实旧版 HTTP handler 续页。
func main() {
	var input struct {
		RedisAddr string
		Storages  map[string]*tsdb.Storage
		Config    map[string]any
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		panic(err)
	}
	metadata.InitMetadata()
	gin.SetMode(gin.ReleaseMode)
	if err := redisUtil.SetInstance(context.Background(), "scroll-version-test", "scroll-version-test", &goRedis.UniversalOptions{
		Addrs: []string{input.RedisAddr}, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second,
	}); err != nil {
		panic(err)
	}
	defer redisUtil.Close()
	for id, storage := range input.Storages {
		tsdb.SetStorage(id, storage)
	}
	for key, value := range input.Config {
		viper.Set(key, value)
	}
	queryHTTP.ScrollWindowTimeout, queryHTTP.ScrollSessionLockTimeout = "5m", "10s"
	queryHTTP.QueryMaxRouting = 4
	tsdbService.EsTimeout, tsdbService.EsMaxSize = 10*time.Second, 100
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	fmt.Println(listener.Addr().String())
	server := &http.Server{
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			ctx := metadata.InitHashID(request.Context())
			metadata.SetUser(ctx, &metadata.User{
				Key: request.Header.Get("Bk-Query-Source"), TenantID: request.Header.Get(metadata.TenantIDHeader), SpaceUID: request.Header.Get(metadata.SpaceUIDHeader),
			})
			ginContext, _ := gin.CreateTestContext(response)
			ginContext.Request = request.WithContext(ctx)
			queryHTTP.HandlerQueryRawWithScroll(ginContext)
		}),
	}
	if err := server.Serve(listener); err != nil {
		panic(err)
	}
}
