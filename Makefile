.PHONY: fmt vet test test-race test-integration build lint-chart check test-container
fmt:
	gofmt -w cmd internal tests testdata
vet:
	go vet ./...
test:
	go test ./...
test-race:
	go test -race ./...
test-integration:
	go test -tags=integration -count=1 -timeout=5m ./tests/integration
build:
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -o bin/mf-account-reloader ./cmd/mf-account-reloader
lint-chart:
	python3 scripts/check-chart.py
check: vet test test-race test-integration lint-chart build
	@test -z "$$(gofmt -l cmd internal tests testdata)"
	actionlint -ignore 'unexpected key "queue" for "concurrency" section'
test-container:
	bash scripts/test-container.sh
