.PHONY: build build-api test e2e clean

build:
	go build -o escrow ./cmd/escrow

build-api:
	go build -o escrow-api ./cmd/escrow-api

test:
	go test ./pkg/escrow/... -count=1 -timeout 120s -v

e2e: build
	./scripts/run-e2e.sh

clean:
	rm -f escrow escrow-api escrow-test
