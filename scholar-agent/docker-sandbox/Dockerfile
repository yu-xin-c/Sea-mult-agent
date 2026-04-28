# 使用官方 Go 镜像作为编译环境
FROM golang:1.22-bullseye AS builder

ENV GOTOOLCHAIN=auto
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod tidy
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/sandbox main.go

# 直接使用官方 Docker CLI 运行时镜像，避免 apt 源不稳定
FROM docker:27-cli
WORKDIR /app
COPY --from=builder /app/bin/sandbox .

# 暴露端口
EXPOSE 8082

# 运行服务
CMD ["./sandbox"]