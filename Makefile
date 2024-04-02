##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Build Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin

## Tool Binaries
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
KCP ?= $(LOCALBIN)/kcp
KUBECTL_KCP ?= $(LOCALBIN)/kubectl-kcp
YQ ?= $(LOCALBIN)/yq

## Tool Versions
KUSTOMIZE_VERSION ?= v3.8.7
CONTROLLER_TOOLS_VERSION ?= v0.14.0
KCP_VERSION ?= 0.23.0
YQ_VERSION ?= v4.27.2

KUSTOMIZE_INSTALL_SCRIPT ?= "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh"
$(KUSTOMIZE): ## Download kustomize locally if necessary.
	mkdir -p $(LOCALBIN)
	curl -s $(KUSTOMIZE_INSTALL_SCRIPT) | bash -s -- $(subst v,,$(KUSTOMIZE_VERSION)) $(LOCALBIN)
	touch $(KUSTOMIZE) # we download an "old" file, so make will re-download to refresh it unless we make it newer than the owning dir

$(CONTROLLER_GEN): ## Download controller-gen locally if necessary.
	mkdir -p $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

$(YQ): ## Download yq locally if necessary.
	mkdir -p $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install github.com/mikefarah/yq/v4@$(YQ_VERSION)

OS ?= $(shell go env GOOS )
ARCH ?= $(shell go env GOARCH )

$(KCP): ## Download kcp locally if necessary.
	mkdir -p $(LOCALBIN)
	curl -L -s -o - https://github.com/kcp-dev/kcp/releases/download/v$(KCP_VERSION)/kcp_$(KCP_VERSION)_$(OS)_$(ARCH).tar.gz | tar --directory $(LOCALBIN)/../ -xvzf - bin/kcp
	touch $(KCP) # we download an "old" file, so make will re-download to refresh it unless we make it newer than the owning dir

$(KUBECTL_KCP): ## Download kcp kubectl plugins locally if necessary.
	mkdir -p $(LOCALBIN)
	curl -L -s -o - https://github.com/kcp-dev/kcp/releases/download/v$(KCP_VERSION)/kubectl-kcp-plugin_$(KCP_VERSION)_$(OS)_$(ARCH).tar.gz | tar --directory $(LOCALBIN)/../ -xvzf - bin
	touch $(KUBECTL_KCP) # we download an "old" file, so make will re-download to refresh it unless we make it newer than the owning dir

$(ENVTEST): ## Download envtest locally if necessary.
	mkdir -p $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest

# Image registry and URL to use all building/pushing image targets
REGISTRY ?= localhost
IMG ?= controller:test
# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.24

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
# This is a requirement for 'setup-envtest.sh' in the test target.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

# kcp specific
APIEXPORT_PREFIX ?= today

##@ Development

.PHONY: manifests
manifests: $(CONTROLLER_GEN) ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: apiresourceschemas
apiresourceschemas: $(KUSTOMIZE) ## Convert CRDs from config/crds to APIResourceSchemas. Specify APIEXPORT_PREFIX as needed.
	$(KUSTOMIZE) build config/crd | kubectl kcp crd snapshot -f - --prefix $(APIEXPORT_PREFIX) > config/kcp/$(APIEXPORT_PREFIX).apiresourceschemas.yaml

.PHONY: generate
generate: $(CONTROLLER_GEN) ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet $(ENVTEST) ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" go test ./controllers/... -coverprofile cover.out

ARTIFACT_DIR ?= .test

.PHONY: test-e2e
test-e2e: $(ARTIFACT_DIR)/kind.kubeconfig kcp-synctarget ready-deployment run-test-e2e## Set up prerequisites and run end-to-end tests on a cluster.

.PHONY: run-test-e2e
run-test-e2e: ## Run end-to-end tests on a cluster.
	go test -v ./test/e2e/... --kubeconfig $(abspath $(ARTIFACT_DIR)/kcp.kubeconfig) --workspace $(shell $(KCP_KUBECTL) get logicalcluster cluster -o jsonpath="{.metadata.annotations.kcp\.io/path}")

.PHONY: ready-deployment
ready-deployment: KUBECONFIG = $(ARTIFACT_DIR)/kcp.kubeconfig
ready-deployment: kind-image install bindcompute deploy apibinding  ## Deploy the controller-manager and wait for it to be ready.
	$(KCP_KUBECTL) --namespace "controller-runtime-example-system" rollout status deployment/controller-runtime-example-controller-manager

.PHONY: bindcompute
bindcompute:
	$(KCP_KUBECTL) kcp bind compute $(shell $(KCP_KUBECTL) get logicalcluster cluster -o jsonpath="{.metadata.annotations.kcp\.io/path}")

# TODO(skuznets|ncdc): this APIBinding is not needed, but here only to work around https://github.com/kcp-dev/kcp/issues/2663 - remove it once that is fixed
.PHONY: apibinding
apibinding:
	$( eval WORKSPACE = $(shell $(KCP_KUBECTL) kcp workspace . --short))
	sed 's/WORKSPACE/$(WORKSPACE)/' ./test/e2e/apibinding.yaml | $(KCP_KUBECTL) apply -f -
	$(KCP_KUBECTL) wait --for=condition=Ready apibinding/data.my.domain

.PHONY: kind-image
kind-image: docker-build ## Load the controller-manager image into the kind cluster.
	kind load docker-image $(REGISTRY)/$(IMG) --name controller-runtime-example

$(ARTIFACT_DIR)/kind.kubeconfig: $(ARTIFACT_DIR) ## Run a kind cluster and generate a $KUBECONFIG for it.
	@if ! kind get clusters --quiet | grep --quiet controller-runtime-example; then kind create cluster --name controller-runtime-example --image kindest/node:v1.24.2; fi
	kind get kubeconfig --name controller-runtime-example > $(ARTIFACT_DIR)/kind.kubeconfig

$(ARTIFACT_DIR): ## Create a directory for test artifacts.
	mkdir -p $(ARTIFACT_DIR)

KCP_KUBECTL ?= PATH=$(LOCALBIN):$(PATH) KUBECONFIG=$(ARTIFACT_DIR)/kcp.kubeconfig kubectl
KIND_KUBECTL ?= kubectl --kubeconfig $(ARTIFACT_DIR)/kind.kubeconfig

.PHONY: kcp-synctarget
kcp-synctarget: kcp-workspace $(ARTIFACT_DIR)/syncer.yaml $(YQ) ## Add the kind cluster to kcp as a target for workloads.
	$(KIND_KUBECTL) apply -f $(ARTIFACT_DIR)/syncer.yaml
	$(eval DEPLOYMENT_NAME = $(shell $(YQ) 'select(.kind=="Deployment") | .metadata.name' < $(ARTIFACT_DIR)/syncer.yaml ))
	$(eval DEPLOYMENT_NAMESPACE = $(shell $(YQ) 'select(.kind=="Deployment") | .metadata.namespace' < $(ARTIFACT_DIR)/syncer.yaml ))
	$(KIND_KUBECTL) --namespace $(DEPLOYMENT_NAMESPACE) rollout status deployment/$(DEPLOYMENT_NAME)
	@if [[ ! -s $(ARTIFACT_DIR)/syncer.log ]]; then ( $(KIND_KUBECTL) --namespace $(DEPLOYMENT_NAMESPACE) logs deployment/$(DEPLOYMENT_NAME) -f >$(ARTIFACT_DIR)/syncer.log 2>&1 & ); fi
	$(KCP_KUBECTL) wait --for=condition=Ready synctarget/controller-runtime

$(ARTIFACT_DIR)/syncer.yaml: ## Generate the manifests necessary to register the kind cluster with kcp.
	$(KCP_KUBECTL) kcp workload sync controller-runtime --resources services --syncer-image ghcr.io/kcp-dev/kcp/syncer:v$(KCP_VERSION) --output-file $(ARTIFACT_DIR)/syncer.yaml

.PHONY: kcp-workspace
kcp-workspace: $(KUBECTL_KCP) kcp-server ## Create a workspace in kcp for the controller-manager.
	$(KCP_KUBECTL) kcp workspace use '~'
	@if ! $(KCP_KUBECTL) kcp workspace use controller-runtime-example; then $(KCP_KUBECTL) kcp workspace create controller-runtime-example --type universal --enter; fi

.PHONY: kcp-server
kcp-server: $(KCP) $(ARTIFACT_DIR)/kcp ## Run the kcp server.
	@if [[ ! -s $(ARTIFACT_DIR)/kcp.log ]]; then ( $(KCP) start -v 5 --root-directory $(ARTIFACT_DIR)/kcp --kubeconfig-path $(ARTIFACT_DIR)/kcp.kubeconfig --audit-log-maxsize 1024 --audit-log-mode=batch --audit-log-batch-max-wait=1s --audit-log-batch-max-size=1000 --audit-log-batch-buffer-size=10000 --audit-log-batch-throttle-burst=15 --audit-log-batch-throttle-enable=true --audit-log-batch-throttle-qps=10 --audit-policy-file ./test/e2e/audit-policy.yaml --audit-log-path $(ARTIFACT_DIR)/audit.log >$(ARTIFACT_DIR)/kcp.log 2>&1 & ); fi
	@while true; do if [[ ! -s $(ARTIFACT_DIR)/kcp.kubeconfig ]]; then sleep 0.2; else break; fi; done
	@while true; do if ! kubectl --kubeconfig $(ARTIFACT_DIR)/kcp.kubeconfig get --raw /readyz >$(ARTIFACT_DIR)/kcp.probe.log 2>&1; then sleep 0.2; else break; fi; done

$(ARTIFACT_DIR)/kcp: ## Create a directory for the kcp server data.
	mkdir -p $(ARTIFACT_DIR)/kcp

.PHONY: test-e2e-cleanup
test-e2e-cleanup: ## Clean up processes and directories from an end-to-end test run.
	kind delete cluster --name controller-runtime-example || true
	rm -rf $(ARTIFACT_DIR) || true
	pkill -sigterm kcp || true
	pkill -sigterm kubectl || true

.PHONY: clean-bins
clean-bins: ## Remove binaries.
	rm -rf $(ARTIFACT_DIR) || true

##@ Build

.PHONY: build
build: generate fmt vet ## Build manager binary.
	go build -o bin/manager main.go

NAME_PREFIX ?= controller-runtime-example-
APIEXPORT_NAME ?= data.my.domain

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./main.go --api-export-name $(NAME_PREFIX)$(APIEXPORT_NAME)

.PHONY: docker-build
docker-build: build ## Build docker image with the manager.
	docker build -t ${REGISTRY}/${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${REGISTRY}/${IMG}

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

KUBECONFIG ?= $(abspath ${HOME}/.kube/config )

.PHONY: install
install: manifests $(KUSTOMIZE) ## Install APIResourceSchemas and APIExport into kcp (using $KUBECONFIG or ~/.kube/config).
	$(KUSTOMIZE) build config/kcp | kubectl --kubeconfig $(KUBECONFIG) apply -f -

.PHONY: uninstall
uninstall: manifests $(KUSTOMIZE) ## Uninstall APIResourceSchemas and APIExport from kcp (using $KUBECONFIG or ~/.kube/config). Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/kcp | kubectl --kubeconfig $(KUBECONFIG) delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy-crd
deploy-crd: manifests $(KUSTOMIZE) ## Deploy controller
	cd config/manager && $(KUSTOMIZE) edit set image controller=${REGISTRY}/${IMG}
	$(KUSTOMIZE) build config/default-crd | kubectl --kubeconfig $(KUBECONFIG) apply -f - || true

.PHONY: deploy
deploy: manifests $(KUSTOMIZE) ## Deploy controller
	cd config/manager && $(KUSTOMIZE) edit set image controller=${REGISTRY}/${IMG}
	$(KUSTOMIZE) build config/default | kubectl --kubeconfig $(KUBECONFIG) apply -f -

.PHONY: undeploy
undeploy: ## Undeploy controller. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/default | kubectl --kubeconfig $(KUBECONFIG) delete --ignore-not-found=$(ignore-not-found) -f -
