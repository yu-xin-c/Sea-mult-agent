# 使用官方 Go 镜像作为编译环境
FROM golang:1.22-bullseye AS builder

ENV GOTOOLCHAIN=auto
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod tidy
COPY . .
RUN go build -o bin/api cmd/api/main.go

# 使用轻量级镜像运行
FROM debian:bullseye-slim
WORKDIR /app
COPY --from=builder /app/bin/api .

# 暴露端口
EXPOSE 8080

# 运行服务
CMD ["./api"]
