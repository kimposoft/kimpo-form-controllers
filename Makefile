# 常用目标：
#   make build   只编译 plugin.bin（本机平台）
#   make test    运行单元测试
#   make kpp     完整构建并打包 .kpp（等同 bash build.sh）
export GOWORK := off

.PHONY: build test kpp clean
build:
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o plugin.bin ./cmd/plugin

test:
	go test ./...

kpp:
	bash build.sh

clean:
	rm -rf plugin.bin release
