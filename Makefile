.PHONY: test vet run fmt check clean

test:
	go test ./...

vet:
	go vet ./...

run:
	go run ./cmd/matching-engine

fmt:
	gofmt -w .

check: fmt vet test

clean:
	rm -rf matching-engine
