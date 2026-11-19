VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PORT    ?= 7070
DATADIR ?= ./data
LDFLAGS := -s -w -X github.com/holacloud/clocme/config.VERSION=$(VERSION)

# docker compose gets the version and the host user (so files in the data
# volume stay owned by you). App settings live in config.json (mounted into
# the container); docker-side settings can go in .env (see .env.example).
COMPOSE := VERSION=$(VERSION) UID=$(shell id -u) GID=$(shell id -g) docker compose

.PHONY: up down restart logs ps rebuild docker-test run build test vet fmt tidy clean help

## config.json: created from config.example.json if it does not exist
config.json:
	cp config.example.json config.json
	@echo "created config.json from config.example.json, edit it to set github_token, telegram..."

## up: build the image and start the service in docker (data and config.json mounted)
up: config.json
	mkdir -p $(DATADIR)
	$(COMPOSE) up -d --build
	@echo "clocme running on http://localhost:$(PORT)"

## down: stop and remove the container (data is kept)
down:
	$(COMPOSE) down

## restart: apply config.json changes (recreates the container, no rebuild)
# Recreate instead of `docker restart`: editors often save by replacing the
# file, and a restarted container would keep seeing the old one.
restart: config.json
	$(COMPOSE) up -d --force-recreate --no-build

## logs: follow the service logs
logs:
	$(COMPOSE) logs -f

## ps: container status and health
ps:
	$(COMPOSE) ps

## rebuild: rebuild the image from scratch and restart
rebuild:
	$(COMPOSE) build --no-cache
	$(COMPOSE) up -d

## docker-test: run the test suite inside a golang container
docker-test:
	docker run --rm -v $(CURDIR):/src -w /src -v clocme-gocache:/root/.cache -v clocme-gomod:/go/pkg/mod \
		golang:1.27 sh -c 'git config --global safe.directory "*" && go vet ./... && go test -race ./...'

## run: (without docker) build and run on the host
run: build
	./clocme -addr :$(PORT) -baseurl http://localhost:$(PORT) -datadir $(DATADIR)

## build: (without docker) compile the binary
build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o clocme .

## test: (without docker) run all tests with the race detector
test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

tidy:
	go mod tidy

## clean: remove the local binary (keeps data)
clean:
	rm -f clocme

help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //'
