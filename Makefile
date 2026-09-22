.PHONY: help init deps run build test fmt vet docker-up docker-down clean

APP := passguard

help: ## Affiche cette aide
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

init: ## Initialise l'environnement de développement (.env, base, deps)
	@bash scripts/init.sh

deps: ## Télécharge les dépendances Go
	go mod tidy

run: ## Démarre le serveur
	go run ./cmd/server

build: ## Compile le binaire dans bin/
	@mkdir -p bin
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/$(APP) ./cmd/server

test: ## Lance les tests unitaires
	go test ./... -v

fmt: ## Formate le code
	gofmt -w .

vet: ## Analyse statique
	go vet ./...

docker-up: ## Démarre PostgreSQL + l'application via Docker
	docker compose up -d --build

docker-down: ## Arrête les conteneurs
	docker compose down

clean: ## Supprime les artefacts de build
	rm -rf bin
