# ScholarAgent development helpers

.PHONY: all help install install-ai tidy lint build package sync-frontend-dist test test-optimizer eval example-paper-reproduction example-autoresearch run-backend run-frontend run-sandbox run-optimizer docker-up docker-down clean

all: help

help:
	@echo "ScholarAgent commands:"
	@echo "  make install       - Install frontend deps and tidy Go modules"
	@echo "  make install-ai    - Install optional Python AI service deps"
	@echo "  make tidy          - Run go mod tidy for backend and sandbox"
	@echo "  make lint          - Run frontend lint"
	@echo "  make build         - Build frontend, backend and sandbox"
	@echo "  make package       - Build a single executable with embedded frontend and sandbox"
	@echo "  make test          - Run backend, sandbox, frontend and offline example tests"
	@echo "  make test-optimizer - Run the Python Research Optimizer tests"
	@echo "  make eval          - Run the deterministic Agent planning evaluation dataset"
	@echo "  make example-paper-reproduction - Run the live Attention smoke example"
	@echo "  make example-autoresearch - Run the frozen intent-router AutoResearch baseline"
	@echo "  make run-sandbox   - Start docker-sandbox locally via scripts/unix/"
	@echo "  make run-optimizer - Start the Python Research Optimizer locally"
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

sync-frontend-dist:
	rm -rf backend/webui/dist
	mkdir -p backend/webui/dist
	cp -R frontend/dist/. backend/webui/dist/
	printf '%s\n' 'This file keeps the embedded frontend directory present for Go builds before' 'the real frontend bundle is generated.' > backend/webui/dist/placeholder.txt

package:
	cd frontend && npm run build
	$(MAKE) sync-frontend-dist
	cd backend && go build -o bin/scholar-agent ./cmd/app

test:
	cd backend && go test ./...
	cd docker-sandbox && go test ./...
	cd frontend && npm test
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s examples/paper-reproduction -p 'test_*.py'
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s examples/autoresearch/intent_router -p 'test_*.py'
	$(MAKE) test-optimizer

test-optimizer:
	cd research-optimizer && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests -p 'test_*.py'

eval:
	cd backend && go test -v ./evals

example-paper-reproduction:
	PYTHONDONTWRITEBYTECODE=1 python3 examples/paper-reproduction/run.py

example-autoresearch:
	PYTHONDONTWRITEBYTECODE=1 python3 examples/autoresearch/intent_router/evaluator.py

run-sandbox:
	./scripts/unix/start-sandbox.sh

run-optimizer:
	cd research-optimizer && RESEARCH_OPTIMIZER_DB_PATH=$${RESEARCH_OPTIMIZER_DB_PATH:-/tmp/scholar-experiences.sqlite3} python3 -m research_optimizer.service

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
