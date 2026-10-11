.PHONY: fmt vet test test-race test-integration build lint-chart check test-container test-workflows
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
test-workflows:
	python3 scripts/test-workflows.py
check: vet test test-race test-integration lint-chart test-workflows build
	@test -z "$$(gofmt -l cmd internal tests testdata)"
	actionlint
test-container:
	bash scripts/test-container.sh
