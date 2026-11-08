# GetContact API — developer shortcuts.
# POSIX shell; on Windows use Git Bash, WSL or run the underlying commands directly.

SHELL      := /bin/sh
GO         ?= go
COMPOSE    ?= docker compose -f deploy/docker/docker-compose.yml
API_URL    ?= http://localhost:8080
IMAGE_TAG  ?= 1.2.0
REGISTRY   ?= ghcr.io/your-org

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-18s\033[0m %s\n", $$1, $$2}'

## --- Go ---------------------------------------------------------------------

.PHONY: tidy build run worker test test-unit test-e2e cover fmt vet lint
tidy: ## Download/verify modules
	$(GO) mod tidy

build: ## Build api and worker binaries into ./bin
	CGO_ENABLED=0 $(GO) build -trimpath -o bin/api ./cmd/api
	CGO_ENABLED=0 $(GO) build -trimpath -o bin/worker ./cmd/worker

run: ## Run API locally (needs Postgres/Redis/Kafka or KAFKA_ENABLED=false)
	$(GO) run ./cmd/api

worker: ## Run Kafka consumer locally
	$(GO) run ./cmd/worker

test: ## Run every test package
	$(GO) test ./...

test-unit: ## Run only unit tests
	$(GO) test ./test/unit/... -v

test-e2e: ## Black-box tests against API_URL
	API_URL=$(API_URL) $(GO) test ./test/integration -v

cover: ## Coverage report for all packages
	$(GO) test ./... -coverprofile=coverage.out
	$(GO) tool cover -func=coverage.out | tail -n 1

fmt: ## Format all Go files
	gofmt -w $$(find . -name '*.go' -not -path './bin/*')

vet: ## Run go vet
	$(GO) vet ./...

lint: fmt vet ## Format + vet

## --- Docker -----------------------------------------------------------------

.PHONY: docker-build docker-up docker-down docker-logs docker-ps docker-clean
docker-build: ## Build api and worker images
	docker build -f deploy/docker/Dockerfile --target api    -t $(REGISTRY)/getcontact-api:$(IMAGE_TAG)    .
	docker build -f deploy/docker/Dockerfile --target worker -t $(REGISTRY)/getcontact-worker:$(IMAGE_TAG) .

docker-up: ## Start the full local stack (build if needed)
	$(COMPOSE) up -d --build

docker-down: ## Stop the stack, keep volumes
	$(COMPOSE) down

docker-logs: ## Follow api + worker logs
	$(COMPOSE) logs -f api worker

docker-ps: ## Show stack status
	$(COMPOSE) ps

docker-clean: ## Stop the stack and delete all volumes
	$(COMPOSE) down -v --remove-orphans

## --- Kubernetes -------------------------------------------------------------

.PHONY: k8s-apply k8s-delete k8s-status k8s-port-forward
k8s-apply: ## Apply core manifests
	kubectl apply -k deploy/k8s

k8s-monitoring: ## Apply ServiceMonitor + PrometheusRule
	kubectl apply -k deploy/k8s -f deploy/k8s/kustomization-monitoring.yaml

k8s-delete: ## Delete the getcontact namespace
	kubectl delete namespace getcontact --ignore-not-found

k8s-status: ## Show pods/services/HPA
	kubectl -n getcontact get pods,svc,hpa,pdb

k8s-port-forward: ## Forward the API to localhost:8080
	kubectl -n getcontact port-forward svc/api 8080:80

## --- Tooling ----------------------------------------------------------------

.PHONY: postman openapi-check clean
postman: ## Regenerate Postman collection + environment
	python scripts/gen_postman.py

openapi-check: ## Parse the OpenAPI document
	python - <<'PY'
import yaml
spec = yaml.safe_load(open("api/openapi.yaml", encoding="utf-8"))
print(f"paths={len(spec['paths'])} schemas={len(spec['components']['schemas'])}")
PY

clean: ## Remove build artifacts
	rm -rf bin coverage.out
