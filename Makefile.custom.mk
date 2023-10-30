##@ Development

.PHONY: clear-envtest-cache
clear-envtest-cache: ## Clear envtest ports cache
	rm -rf "$(HOME)/.cache/kubebuilder-envtest/"

.PHONY: test-unit
test-unit: ginkgo generate fmt vet envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" $(GINKGO) -p --nodes 4 --cover -r -randomize-all --randomize-suites --skip-package=tests ./...

.PHONY: test-all
test-all: test-unit  ## Run all tests and litner

##@ Build Dependencies

CONTROLLER_GEN = $(shell pwd)/bin/controller-gen
.PHONY: controller-gen
controller-gen: ## Download controller-gen locally if necessary.
	$(call go-get-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen@v0.9.0)

ENVTEST = $(shell pwd)/bin/setup-envtest
.PHONY: envtest
envtest: clear-envtest-cache ## Download envtest-setup locally if necessary.
	$(call go-get-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest@latest)

GINKGO = $(shell pwd)/bin/ginkgo
.PHONY: ginkgo
ginkgo: ## Download ginkgo locally if necessary.
	$(call go-get-tool,$(GINKGO),github.com/onsi/ginkgo/v2/ginkgo@latest)

ARCHITECT = $(shell pwd)/bin/architect
.PHONY: architect
architect: ## Download architect locally if necessary.
	$(call go-get-tool,$(ARCHITECT),github.com/giantswarm/architect@latest)

KIND = $(shell pwd)/bin/kind
.PHONY: kind
kind: ## Download kind locally if necessary.
	$(call go-get-tool,$(KIND),sigs.k8s.io/kind@latest)

GOIMPORTS = $(shell pwd)/bin/goimports
.PHONY: goimports
goimports: ## Download kind locally if necessary.
	$(call go-get-tool,$(GOIMPORTS),golang.org/x/tools/cmd/goimports@latest)


# go-get-tool will 'go get' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-get-tool
@[ -f $(1) ] || { \
set -e ;\
TMP_DIR=$$(mktemp -d) ;\
cd $$TMP_DIR ;\
go mod init tmp ;\
echo "Downloading $(2)" ;\
GOBIN=$(PROJECT_DIR)/bin go install $(2) ;\
rm -rf $$TMP_DIR ;\
}
endef
