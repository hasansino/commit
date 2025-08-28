# ╭────────────────────----------------──────────╮
# │                    commit                    │
# ╰─────────────────────----------------─────────╯

.PHONY: help
help: Makefile
	@sed -n 's/^##//p' $< | awk 'BEGIN {FS = "|"}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

## setup | install dependencies
setup:
	@go mod tidy -e && go mod download
	@go install go.uber.org/mock/mockgen@latest

## setup-release | install tools for release process
setup-release:
	@go install github.com/goreleaser/goreleaser/v2@latest
	@go install github.com/anchore/syft/cmd/syft@latest
	@go install github.com/sigstore/cosign/v2/cmd/cosign@latest

# ╭────────────────────----------------──────────╮
# │               General workflow               │
# ╰─────────────────────----------------─────────╯

## test | run unit tests
# -count=1 is needed to prevent caching of test results.
test:
	@go test -count=1 -v -race $(shell go list ./... | grep -v './tests')

## build | build development version of binary
build:
	@go build -gcflags="all=-N -l" -race -v -o ./build/commit .
	@file -h ./build/commit && du -h ./build/commit && sha256sum ./build/commit && go tool buildid ./build/commit

## image | build docker image
# @see https://reproducible-builds.org/docs/source-date-epoch/
image:
	@export SOURCE_DATE_EPOCH=0 && \
	docker buildx build --no-cache --platform linux/amd64,linux/arm64 \
    --build-arg "GO_VERSION=$(shell grep '^go ' go.mod | awk '{print $$2}')" \
    --build-arg "COMMIT_HASH=$(shell git rev-parse HEAD 2>/dev/null || echo '')" \
    --build-arg "RELEASE_TAG=$(shell git describe --tags --abbrev=0 2>/dev/null || echo '')" \
	-t ghcr.io/hasansino/commit:dev \
	.

## generate | generate code for all modules
# Side effects of this command should to be commited.
generate:
	@go mod tidy -e
	@go generate ./...

# ╭────────────────────----------------──────────╮
# │                   Release                    │
# ╰─────────────────────----------------─────────╯

## release-check | validate goreleaser configuration
release-check:
	@goreleaser --config .goreleaser.yaml check

## release-snapshot | build release artifacts without publishing
release-snapshot:
	@goreleaser --config .goreleaser.yaml release --snapshot --clean --skip=publish,sign

## release-local | test the full release process locally
release-local:
	@goreleaser --config .goreleaser.yaml release --skip=publish --clean
