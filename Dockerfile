# ---- 构建阶段 ----
FROM golang:1.24-alpine AS builder

WORKDIR /build

# 缓存依赖
COPY server/go.mod ./
RUN go mod download

# 编译
COPY server/*.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o server .

# ---- 运行阶段 ----
FROM alpine:3.20

WORKDIR /app

# 安装 ca-certificates 以便 HTTPS 请求 Data Dragon
RUN apk add --no-cache ca-certificates tzdata

# 复制二进制和前端
COPY --from=builder /build/server ./build/server
COPY web/ ./web/

# 复制已缓存的官方图片资源 (镜像自带, 开箱即用)
COPY assets/ ./assets/

# 创建保存目录 (保存的JSON配置)
RUN mkdir -p assets/saves

EXPOSE 8765

# 健康检查
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- http://localhost:8765/api/version || exit 1

CMD ["./build/server"]
