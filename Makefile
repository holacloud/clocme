VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PORT    ?= 7070
DATADIR ?= ./data
LDFLAGS := -s -w -X github.com/holacloud/clocme/config.VERSION=$(VERSION)

.PHONY: run build test vet fmt tidy clean docker-build docker-run

## run: build and run locally on PORT (default 7070)
run: build
	./clocme -addr :$(PORT) -baseurl http://localhost:$(PORT) -datadir $(DATADIR)

## build: compile the binary
build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o clocme .

## test: run all tests with the race detector
test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

tidy:
	go mod tidy

## clean: remove binary (keeps data)
clean:
	rm -f clocme

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t clocme:$(VERSION) -t clocme:latest .

docker-run: docker-build
	docker run --rm -p $(PORT):8080 -v clocme-data:/data clocme:latest

help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //'
