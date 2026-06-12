.PHONY: build run test lint swagger genkey

build:
	go build -o bin/hivemind ./cmd/server

run:
	go run ./cmd/server

test:
	go test ./... -count=1

lint:
	go vet ./...

# Regenerate Swagger docs from annotations.
# Requires: go install github.com/swaggo/swag/cmd/swag@latest
swagger:
	swag init \
		--generalInfo cmd/server/main.go \
		--output docs \
		--parseDependency \
		--parseInternal

# Generate a fresh Ed25519 private key.
genkey:
	go run ./cmd/genkey -out certs/private.pem
