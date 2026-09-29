# Copyright 2026 Rungar Authors
# SPDX-License-Identifier: MIT

SHELL := /usr/bin/env bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := help

# Rungar is a network client: it talks to its providers -- Dicer daemons over
# gRPC, and the AWS, GCP and Proxmox APIs -- and to GitHub over HTTPS, and does
# nothing that needs a particular kernel. It is built for
# whatever machine builds it unless told otherwise, e.g. `make build
# GOOS=linux GOARCH=arm64`.
GOOS   ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

# The platforms released binaries are built for.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

BIN_DIR ?= $(CURDIR)/bin

# The container image, for running Rungar somewhere that is not a machine you
# keep binaries on. Releases publish it; see .github/workflows/image.yaml.
IMAGE     ?= ghcr.io/konradasb/rungar
IMAGE_TAG ?= $(VERSION)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/konradasb/rungar/internal/version.Version=$(VERSION) \
	-X github.com/konradasb/rungar/internal/version.Commit=$(COMMIT) \
	-X github.com/konradasb/rungar/internal/version.BuildDate=$(DATE)

# Tools, pinned so every machine lints identically. They are built with at
# least the toolchain go.mod selects: a linter built with an older Go than
# this module's refuses to load it.
GO_TOOLCHAIN := $(shell go env GOVERSION)
ADDLICENSE_VERSION    := v1.2.0
GOLANGCI_LINT_VERSION := v2.8.0
GOVULNCHECK_VERSION   := v1.8.0
ACTIONLINT_VERSION    := v1.7.12
GORELEASER_VERSION    := v2.12.3
HUGO_VERSION          := v0.166.0
BUF_VERSION           := v1.73.0
HELM_VERSION          := v4.3.0
KIND_VERSION          := v0.33.0
# zizmor and Checkov are not Go tools; they run through pipx, which fetches
# them on first use.
ZIZMOR_VERSION        := 1.30.1
CHECKOV_VERSION       := 3.3.22

ADDLICENSE    := $(BIN_DIR)/addlicense-$(ADDLICENSE_VERSION)
GOLANGCI_LINT := $(BIN_DIR)/golangci-lint-$(GOLANGCI_LINT_VERSION)
GOVULNCHECK   := $(BIN_DIR)/govulncheck-$(GOVULNCHECK_VERSION)
ACTIONLINT    := $(BIN_DIR)/actionlint-$(ACTIONLINT_VERSION)
GORELEASER    := $(BIN_DIR)/goreleaser-$(GORELEASER_VERSION)
HUGO          := $(BIN_DIR)/hugo-$(HUGO_VERSION)
BUF           := $(BIN_DIR)/buf-$(BUF_VERSION)
HELM          := $(BIN_DIR)/helm-$(HELM_VERSION)
KIND          := $(BIN_DIR)/kind-$(KIND_VERSION)

LICENSE_IGNORE := -ignore 'bin/**' -ignore '**/bin/**' -ignore 'docs/public/**' -ignore 'docs/resources/**' -ignore 'docs/site/**' -ignore 'dist/**' -ignore 'completions/**'

##@ Building

.PHONY: build
build: ## Build rungar into bin/ for this machine (override with GOOS/GOARCH)
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath \
		-ldflags '$(LDFLAGS)' -o $(BIN_DIR)/rungar ./cmd/rungar

.PHONY: build-all
build-all: ## Build rungar for every released platform, into bin/
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		echo "  $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath \
			-ldflags '$(LDFLAGS)' -o $(BIN_DIR)/rungar_$${os}_$${arch} ./cmd/rungar || exit 1; \
	done

.PHONY: docker
docker: ## Build the container image for this machine's architecture
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(DATE) \
		-t $(IMAGE):$(IMAGE_TAG) .
	@echo "built $(IMAGE):$(IMAGE_TAG)"

.PHONY: release-snapshot
release-snapshot: $(GORELEASER) ## Build the release archives into dist/, as a release does, unsigned
	$(GORELEASER) release --snapshot --clean --skip=sign,sbom

COMPLETIONS_DIR := completions

.PHONY: completions
completions: ## Generate rungar's shell completions into completions/ (run by GoReleaser)
	@mkdir -p $(COMPLETIONS_DIR)
	go run ./cmd/rungar completion bash > $(COMPLETIONS_DIR)/rungar.bash
	go run ./cmd/rungar completion zsh > $(COMPLETIONS_DIR)/_rungar
	go run ./cmd/rungar completion fish > $(COMPLETIONS_DIR)/rungar.fish

# The deb and rpm packages of every architecture, as a release builds them,
# into dist/, unsigned. build/package holds what they install.
.PHONY: packages
packages: $(GORELEASER) ## Build the Linux packages into dist/, as a release does
	$(GORELEASER) release --snapshot --clean --skip=sign,sbom,archive

.PHONY: test-packages
test-packages: packages ## Install the packages from a test repository on each distribution, in docker
	build/test-packages.sh dist

# The Helm chart. A release packages it with CHART_VERSION set to its own
# version, which is both the chart's and the image it runs; unset, it is what
# Chart.yaml says, which runs the image tagged latest.
CHART_DIR     := charts/rungar
CHART_VERSION ?=

.PHONY: chart
chart: $(HELM) ## Package the Helm chart into dist/
	$(HELM) package $(CHART_DIR) -d dist \
		$(if $(CHART_VERSION),--version $(CHART_VERSION) --app-version $(CHART_VERSION))

.PHONY: lint-chart
# Checkov scans what the chart renders, with each values file, rather than the
# chart itself, which it would render with the defaults: those set no
# configuration, which the chart refuses.
lint-chart: $(HELM) ## Lint the Helm chart, and scan it for misconfigurations, with each of its test values files
	@for values in $(CHART_DIR)/ci/*-values.yaml; do \
		$(HELM) lint --strict $(CHART_DIR) -f $$values || exit 1; \
	done
	@rendered=$$(mktemp -d); trap 'rm -rf "$$rendered"' EXIT; \
	for values in $(CHART_DIR)/ci/*-values.yaml; do \
		$(HELM) template rungar $(CHART_DIR) -f $$values >"$$rendered/$$(basename $$values)"; \
	done; \
	pipx run checkov==$(CHECKOV_VERSION) --config-file charts/checkov.yaml -d "$$rendered"

.PHONY: test-chart
test-chart: $(HELM) $(KIND) ## Install the Helm chart in a kind cluster, with the image built from here
	HELM=$(HELM) KIND=$(KIND) charts/test-chart.sh

##@ Testing

.PHONY: test
test: ## Run the unit tests
	go test -race -short ./...

# FUZZTIME is how long make fuzz runs each fuzz test.
FUZZTIME ?= 1m

.PHONY: fuzz
fuzz: ## Fuzz the configuration's parsing and editing, each fuzz test for FUZZTIME
	go test -run '^$$' -fuzz '^FuzzLoad$$' -fuzztime $(FUZZTIME) ./internal/config
	go test -run '^$$' -fuzz '^FuzzEditVersion$$' -fuzztime $(FUZZTIME) ./internal/config
	go test -run '^$$' -fuzz '^FuzzResolve$$' -fuzztime $(FUZZTIME) ./internal/provider
	go test -run '^$$' -fuzz '^FuzzMerge$$' -fuzztime $(FUZZTIME) ./internal/provider

.PHONY: cover
cover: ## Run the unit tests and report coverage per package
	go test -race -short -coverprofile=coverage.out -covermode=atomic ./...
	@go tool cover -func=coverage.out | tail -1

.PHONY: cover-html
cover-html: cover ## Open the coverage report in a browser
	go tool cover -html=coverage.out

.PHONY: test-e2e
test-e2e: ## Run rungar on a real host against its real providers and GitHub (needs RUNGAR_E2E_HOST; see DEVELOPMENT.md)
	@test -n "$(RUNGAR_E2E_HOST)" || { echo "set RUNGAR_E2E_HOST=<host> to run the end-to-end tests"; exit 1; }
	RUNGAR_E2E_HOST='$(RUNGAR_E2E_HOST)' \
	RUNGAR_E2E_USER='$(RUNGAR_E2E_USER)' \
	RUNGAR_E2E_KEY='$(RUNGAR_E2E_KEY)' \
	RUNGAR_E2E_CONFIG='$(RUNGAR_E2E_CONFIG)' \
	RUNGAR_E2E_PROVIDERS='$(RUNGAR_E2E_PROVIDERS)' \
	RUNGAR_E2E_KEEP='$(RUNGAR_E2E_KEEP)' \
	go test -tags e2e -count 1 -v -timeout 60m ./test/e2e/...

.PHONY: lint
lint: $(GOLANGCI_LINT) $(BUF) ## Run the linters
	$(GOLANGCI_LINT) run ./...
	$(BUF) lint
	# The end-to-end tests are behind a build tag, so the default run does
	# not see them at all. Linted separately rather than left to rot.
	$(GOLANGCI_LINT) run --build-tags e2e ./test/...

.PHONY: lint-workflows
lint-workflows: $(ACTIONLINT) ## Lint the GitHub Actions workflows, for mistakes and for security
	$(ACTIONLINT)
	pipx run zizmor==$(ZIZMOR_VERSION) .

.PHONY: vuln
vuln: $(GOVULNCHECK) ## Check for known vulnerabilities in the code the binary calls
	$(GOVULNCHECK) ./...

.PHONY: fmt
fmt: $(GOLANGCI_LINT) $(BUF) ## Format the code
	$(GOLANGCI_LINT) fmt ./...
	$(BUF) format -w

# What the API is checked against for changes that break its clients: main,
# unless told otherwise.
BUF_AGAINST ?= .git\#branch=main

.PHONY: breaking
breaking: $(BUF) ## Check the API for changes that break the command line of an older release
	$(BUF) breaking --against '$(BUF_AGAINST)'

.PHONY: generate
generate: $(BUF) ## Regenerate the gRPC code from proto/
	$(BUF) generate

.PHONY: license-check
license-check: $(ADDLICENSE) ## Check every source file carries a licence header
	$(ADDLICENSE) -check -c "Rungar Authors" -l mit -s=only $(LICENSE_IGNORE) .

.PHONY: license
license: $(ADDLICENSE) ## Add missing licence headers
	$(ADDLICENSE) -c "Rungar Authors" -l mit -s=only $(LICENSE_IGNORE) .

##@ Documentation

# The site is Hugo with the Hextra theme, which is a Hugo module pinned in
# docs/go.mod; Hugo fetches it on the first build.
DOCS_DIR := $(CURDIR)/docs

.PHONY: docs-gen
docs-gen: ## Generate the documentation site's reference pages: the command line and the configuration
	go run ./tools/docgen

.PHONY: docs
docs: $(HUGO) ## Build the documentation site into docs/public
	cd $(DOCS_DIR) && $(HUGO) --gc --minify

.PHONY: docs-versions
docs-versions: $(HUGO) ## Build every version in docs/versions into docs/site, as the site is published
	$(DOCS_DIR)/build-versions.sh $(HUGO) $(DOCS_DIR)/site

.PHONY: docs-serve
docs-serve: $(HUGO) ## Serve the documentation site at http://localhost:1313, rebuilding on change
	cd $(DOCS_DIR) && $(HUGO) server --renderToMemory

##@ Tools

$(ADDLICENSE): | $(BIN_DIR)
	GOTOOLCHAIN=$(GO_TOOLCHAIN)+auto GOBIN=$(BIN_DIR) go install github.com/google/addlicense@$(ADDLICENSE_VERSION)
	mv $(BIN_DIR)/addlicense $@

$(GOLANGCI_LINT): | $(BIN_DIR)
	GOTOOLCHAIN=$(GO_TOOLCHAIN)+auto GOBIN=$(BIN_DIR) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	mv $(BIN_DIR)/golangci-lint $@

$(GOVULNCHECK): | $(BIN_DIR)
	GOTOOLCHAIN=$(GO_TOOLCHAIN)+auto GOBIN=$(BIN_DIR) go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	mv $(BIN_DIR)/govulncheck $@

$(ACTIONLINT): | $(BIN_DIR)
	GOTOOLCHAIN=$(GO_TOOLCHAIN)+auto GOBIN=$(BIN_DIR) go install github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)
	mv $(BIN_DIR)/actionlint $@

$(GORELEASER): | $(BIN_DIR)
	GOTOOLCHAIN=$(GO_TOOLCHAIN)+auto GOBIN=$(BIN_DIR) go install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)
	mv $(BIN_DIR)/goreleaser $@

$(HUGO): | $(BIN_DIR)
	GOTOOLCHAIN=$(GO_TOOLCHAIN)+auto GOBIN=$(BIN_DIR) go install github.com/gohugoio/hugo@$(HUGO_VERSION)
	mv $(BIN_DIR)/hugo $@

$(BUF): | $(BIN_DIR)
	GOTOOLCHAIN=$(GO_TOOLCHAIN)+auto GOBIN=$(BIN_DIR) go install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	mv $(BIN_DIR)/buf $@

$(HELM): | $(BIN_DIR)
	GOTOOLCHAIN=$(GO_TOOLCHAIN)+auto GOBIN=$(BIN_DIR) go install helm.sh/helm/v4/cmd/helm@$(HELM_VERSION)
	mv $(BIN_DIR)/helm $@

$(KIND): | $(BIN_DIR)
	GOTOOLCHAIN=$(GO_TOOLCHAIN)+auto GOBIN=$(BIN_DIR) go install sigs.k8s.io/kind@$(KIND_VERSION)
	mv $(BIN_DIR)/kind $@

$(BIN_DIR):
	mkdir -p $@

.PHONY: tools
tools: $(ADDLICENSE) $(GOLANGCI_LINT) $(GOVULNCHECK) $(ACTIONLINT) $(GORELEASER) $(HUGO) $(BUF) $(HELM) $(KIND) ## Install the pinned development tools into bin/

##@ Deployment

# A single development host that already runs rungar as a systemd unit (see
# scripts/install.sh); deploy swaps the binary and the unit, as an upgrade by
# install.sh would, and restarts it. The unit goes with the binary because
# the binary may need what only the new unit gives it: /var/log/rungar.
DEPLOY_HOST   ?= root@10.10.0.101
DEPLOY_GOARCH ?= amd64
DEPLOY_DIR    := $(BIN_DIR)/deploy

.PHONY: deploy
deploy: ## Build rungar for Linux and install it and its unit on DEPLOY_HOST, restarting rungar
	CGO_ENABLED=0 GOOS=linux GOARCH=$(DEPLOY_GOARCH) go build -trimpath -ldflags '$(LDFLAGS)' \
		-o $(DEPLOY_DIR)/rungar ./cmd/rungar
	ssh $(DEPLOY_HOST) 'mkdir -p /tmp/rungar-deploy'
	scp $(DEPLOY_DIR)/rungar scripts/rungar.service $(DEPLOY_HOST):/tmp/rungar-deploy/
	# install(1) unlinks the old file first, so replacing the running rungar
	# does not fail with "text file busy".
	ssh $(DEPLOY_HOST) 'set -e; \
		install -m 755 /tmp/rungar-deploy/rungar /usr/local/bin/; \
		install -m 644 /tmp/rungar-deploy/rungar.service /etc/systemd/system/; \
		rm -rf /tmp/rungar-deploy; \
		systemctl daemon-reload; \
		systemctl restart rungar; \
		sleep 1; systemctl is-active rungar'

##@ Housekeeping

.PHONY: clean
clean: ## Remove build output, downloaded binaries and coverage reports
	rm -rf $(BIN_DIR) dist $(COMPLETIONS_DIR) coverage.out docs/public docs/site docs/resources

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-28s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

.PHONY: print-image-tag
print-image-tag: ## Print the image tag `make docker` builds
	@echo $(IMAGE):$(IMAGE_TAG)
