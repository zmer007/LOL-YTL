.PHONY: build run docker-build docker-up docker-down docker-logs clean

# 编译到 build/ 目录
build:
	cd server && go build -o ../build/server .

# 本地运行
run: build
	./build/server

# 构建 Docker 镜像
docker-build:
	docker build -t lol-infographic-editor .

# 启动 Docker 容器
docker-up:
	docker compose up -d

# 停止 Docker 容器
docker-down:
	docker compose down

# 查看日志
docker-logs:
	docker compose logs -f

# 清理编译产物
clean:
	rm -rf build/server
