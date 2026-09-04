.PHONY: build test check release
build:
	go build -trimpath -o bin/codex-setup-$$(go env GOOS)-$$(go env GOARCH) .
test:
	go test ./...
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s payload/integrations/tests -v
check:
	go vet ./...
release:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o bin/codex-setup-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o bin/codex-setup-linux-arm64 .
