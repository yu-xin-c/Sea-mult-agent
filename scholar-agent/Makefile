# ScholarAgent 一键配置与启动工具

.PHONY: all install build run-backend run-frontend run-sandbox run-all clean help

# 默认目标
all: help

help:
	@echo "ScholarAgent 管理命令:"
	@echo "  make install      - 安装所有服务的依赖 (Go & Node.js)"
	@echo "  make build        - 编译所有后端服务"
	@echo "  make run-sandbox  - 启动 Docker 沙箱服务 (监听 :8082)"
	@echo "  make run-backend  - 启动 Go 后端调度引擎 (监听 :8080)"
	@echo "  make run-frontend - 启动 React 前端界面 (监听 :5173)"
	@echo "  make run-all      - [推荐] 同时启动所有服务 (后台运行)"
	@echo "  make stop-all     - 停止所有正在运行的服务"
	@echo "  make clean        - 清理编译产物"

# 1. 安装依赖
install:
	@echo ">>> 正在安装 Go 后端依赖..."
	cd backend && go mod tidy
	@echo ">>> 正在安装 Docker 沙箱服务依赖..."
	cd docker-sandbox && go mod tidy
	@echo ">>> 正在安装 React 前端依赖..."
	cd frontend && npm install

# 2. 编译服务
build:
	@echo ">>> 正在编译后端服务..."
	cd backend && go build -o bin/api cmd/api/main.go
	cd docker-sandbox && go build -o bin/sandbox main.go

# 3. 独立启动命令
run-sandbox:
	@echo ">>> 启动 Docker 沙箱服务..."
	cd docker-sandbox && go run main.go

run-backend:
	@echo ">>> 启动 Go 后端调度引擎..."
	cd backend && go run cmd/api/main.go

run-frontend:
	@echo ">>> 启动 React 前端界面..."
	cd frontend && npm run dev

# 4. 一键启动 (使用 nohup 后台运行并输出日志)
run-all:
	@echo ">>> 正在一键启动全链路服务..."
	@mkdir -p logs
	@echo "1. 启动 Docker 沙箱服务 (日志见 logs/sandbox.log)..."
	cd docker-sandbox && nohup go run main.go > ../logs/sandbox.log 2>&1 &
	@sleep 2
	@echo "2. 启动 Go 后端引擎 (日志见 logs/backend.log)..."
	cd backend && nohup go run cmd/api/main.go > ../logs/backend.log 2>&1 &
	@sleep 2
	@echo "3. 启动前端界面..."
	cd frontend && npm run dev

# 5. 停止所有服务
stop-all:
	@echo ">>> 正在停止所有服务..."
	-pkill -f "go run cmd/api/main.go"
	-pkill -f "go run main.go"
	-pkill -f "vite"
	@echo ">>> 已停止。"

clean:
	rm -rf backend/bin docker-sandbox/bin logs
