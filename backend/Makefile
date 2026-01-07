.PHONY: build test test-unit test-integration test-e2e sync-version

# 从上游获取最新版本号
sync-version:
	@echo "Fetching latest version from upstream..."
	@VERSION=$$(curl -s https://api.github.com/repos/Wei-Shaw/sub2api/releases/latest | grep '"tag_name"' | sed -E 's/.*"v([^"]+)".*/\1/') && \
	if [ -n "$$VERSION" ]; then \
		echo "$$VERSION" > cmd/server/VERSION && \
		echo "Updated VERSION to $$VERSION"; \
	else \
		echo "Failed to fetch version, keeping current"; \
	fi

build: sync-version
	go build -o bin/server ./cmd/server

test:
	go test ./...
	golangci-lint run ./...

test-unit:
	go test -tags=unit ./...

test-integration:
	go test -tags=integration ./...

test-e2e:
	go test -tags=e2e ./...
