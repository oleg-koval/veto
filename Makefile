BIN := veto
CMD := ./cmd/veto

.PHONY: build install test lint clean

build:
	go build -o $(BIN) $(CMD)

install:
	go install $(CMD)

test:
	go test ./...

lint:
	go vet ./...

clean:
	rm -f $(BIN)
