.PHONY: fmt test api indexer analyzer

# 格式化全部 Go 源码。
fmt:
	gofmt -w cmd internal

# 执行单元测试和集成测试。
test:
	go test ./...

# 启动查询 API 和分析任务接口。
api:
	go run ./cmd/api

# 启动链上区块采集器。
indexer:
	go run ./cmd/indexer

# 对指定地址运行一次资金流关系分析，参数通过 ARGS 传入。
analyzer:
	go run ./cmd/analyzer $(ARGS)
