.PHONY: dev build test web-build web-dev go-test

dev:
	./scripts/dev.sh

build: web-build
	go build ./cmd/broute

test: go-test web-build

go-test:
	go test ./...

web-build:
	cd web && npm run build

web-dev:
	cd web && npm run dev
