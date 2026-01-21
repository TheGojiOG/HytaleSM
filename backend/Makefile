.PHONY: build run test clean migrate dev install

# Build the application
build:
	go build -o bin/server cmd/server/main.go

# Run the application
run: build
	./bin/server

# Run in development mode with hot reload (requires air: go install github.com/cosmtrek/air@latest)
dev:
	air

# Run tests
test:
	go test -v ./...

# Run tests with coverage
test-coverage:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Install dependencies
install:
	go mod download
	go mod tidy

# Clean build artifacts
clean:
	rm -rf bin/
	rm -f coverage.out coverage.html

# Run database migrations
migrate:
	go run cmd/server/main.go migrate

# Format code
fmt:
	go fmt ./...

# Run linter (requires golangci-lint)
lint:
	golangci-lint run

# Generate mocks (requires mockgen)
mocks:
	go generate ./...

# Build for multiple platforms
build-all:
	GOOS=linux GOARCH=amd64 go build -o bin/server-linux-amd64 cmd/server/main.go
	GOOS=darwin GOARCH=amd64 go build -o bin/server-darwin-amd64 cmd/server/main.go
	GOOS=windows GOARCH=amd64 go build -o bin/server-windows-amd64.exe cmd/server/main.go

# Docker build
docker-build:
	docker build -f ../docker/Dockerfile.backend -t hytale-manager-backend .

# Docker run
docker-run:
	docker run -p 8080:8080 -v $(PWD)/data:/data -v $(PWD)/configs:/configs hytale-manager-backend
