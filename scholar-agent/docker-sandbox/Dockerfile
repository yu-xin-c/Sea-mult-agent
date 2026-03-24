# 使用官方 Go 镜像作为编译环境
FROM golang:1.22-bullseye AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod tidy
COPY . .
RUN go build -o bin/sandbox main.go

# 使用带有 Docker CLI 的镜像，方便调试（虽然我们使用 SDK）
FROM debian:bullseye-slim
WORKDIR /app
COPY --from=builder /app/bin/sandbox .

# 暴露端口
EXPOSE 8082

# 运行服务
CMD ["./sandbox"]
