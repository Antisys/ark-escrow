.PHONY: build test e2e clean

build:
	go build -o escrow ./cmd/escrow

test:
	go test ./pkg/escrow/... -count=1 -timeout 120s -v

e2e: build
	./scripts/run-e2e.sh

clean:
	rm -f escrow escrow-test
