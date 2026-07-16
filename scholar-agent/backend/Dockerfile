# 使用 Go 构建镜像；GOTOOLCHAIN 会按 go.mod 自动选择项目要求的版本
FROM golang:1.22-bullseye AS builder

ENV GOTOOLCHAIN=auto \
    GOPROXY=https://goproxy.cn,direct
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o bin/api cmd/api/main.go

# 使用轻量级镜像运行
FROM debian:bullseye-slim
WORKDIR /app
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /app/bin/api .

# 暴露端口
EXPOSE 8080

# 运行服务
CMD ["./api"]
