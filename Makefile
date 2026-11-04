SHELL := /bin/sh

.PHONY: help install dev-up dev-down migrate api worker web build test typecheck fmt up down logs

help:
	@echo "install     Install frontend dependencies"
	@echo "dev-up      Start local infrastructure (postgres, redis, nats, stalwart, minio)"
	@echo "dev-down    Stop local infrastructure"
	@echo "migrate     Apply database migrations"
	@echo "api         Run the Go API on :8080"
	@echo "worker      Run the delivery worker and scheduler"
	@echo "web         Run the Next.js dashboard on :3000"
	@echo "build       Build Go binaries and the Next.js app"
	@echo "test        Run Go tests"
	@echo "typecheck   Type-check the frontend"
	@echo "up          Build and start the full Docker stack"
	@echo "down        Stop the full Docker stack"
	@echo "logs        Tail Docker stack logs"

install:
	npm --prefix apps/web install

dev-up:
	docker compose up -d postgres redis nats stalwart minio

dev-down:
	docker compose down

migrate:
	go run ./cmd/migrate

api:
	go run ./cmd/api

worker:
	go run ./cmd/worker

web:
	npm --prefix apps/web run dev

build:
	go build ./...
	npm --prefix apps/web run build

test:
	go test ./...

typecheck:
	npm --prefix apps/web run typecheck

fmt:
	gofmt -w cmd internal

up:
	docker compose up --build -d

down:
	docker compose down

logs:
	docker compose logs -f --tail=200
