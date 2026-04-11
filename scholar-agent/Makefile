# ScholarAgent development helpers

.PHONY: all help install install-ai tidy lint build test run-backend run-frontend run-sandbox docker-up docker-down clean

all: help

help:
	@echo "ScholarAgent commands:"
	@echo "  make install       - Install frontend deps and tidy Go modules"
	@echo "  make install-ai    - Install optional Python AI service deps"
	@echo "  make tidy          - Run go mod tidy for backend and sandbox"
	@echo "  make lint          - Run frontend lint"
	@echo "  make build         - Build frontend, backend and sandbox"
	@echo "  make test          - Run Go tests for backend and sandbox"
	@echo "  make run-sandbox   - Start docker-sandbox locally via scripts/unix/"
	@echo "  make run-backend   - Start backend locally via scripts/unix/"
	@echo "  make run-frontend  - Start frontend locally via scripts/unix/"
	@echo "  make docker-up     - Start services with docker compose via scripts/unix/"
	@echo "  make docker-down   - Stop docker compose services via scripts/unix/"
	@echo "  make clean         - Remove repo-local build artifacts"

install: tidy
	cd frontend && npm install

install-ai:
	cd ai-services/intent_recognition && pip install -r requirements.txt

tidy:
	cd backend && go mod tidy
	cd docker-sandbox && go mod tidy

lint:
	cd frontend && npm run lint

build:
	cd frontend && npm run build
	cd backend && go build -o bin/api ./cmd/api
	cd docker-sandbox && go build -o bin/sandbox .

test:
	cd backend && go test ./...
	cd docker-sandbox && go test ./...

run-sandbox:
	./scripts/unix/start-sandbox.sh

run-backend:
	./scripts/unix/start-backend.sh

run-frontend:
	./scripts/unix/start-frontend.sh

docker-up:
	./scripts/unix/docker-up.sh

docker-down:
	./scripts/unix/docker-down.sh

clean:
	rm -rf .gocache backend/bin docker-sandbox/bin frontend/dist logs
