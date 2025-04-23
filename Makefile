# Define ECR repository names based on project name
ECR_SERVER_REPO_NAME = $(PROJECT_NAME)-server
ECR_WORKER_REPO_NAME = $(PROJECT_NAME)-worker
ECR_CLEANUP_REPO_NAME = $(PROJECT_NAME)-cleanup

# Dockerfiles
SERVER_DOCKERFILE = docker/Dockerfile.server
WORKER_DOCKERFILE = docker/Dockerfile.worker
CLEANUP_DOCKERFILE = docker/Dockerfile.cleanup

# Kubernetes manifests
KUBERNETES_SERVER_FILE = kubernetes/server.yml

# Directory for binaries
BIN_DIR = bin


# --- ECR Push Targets ---

# Login to AWS ECR
.PHONY: ecr-login
ecr-login:
	@echo "Logging into AWS ECR $(AWS_REGISTRY_URL)..."
	@aws ecr get-login-password --region $(AWS_REGION) | docker login --username AWS --password-stdin $(AWS_REGISTRY_URL)

# Ensure ECR repositories exist
# Combine repo checks into one target for potential future use or simplification
.PHONY: ensure-ecr-repos
ensure-ecr-repos: ensure-ecr-repo-server ensure-ecr-repo-worker ensure-ecr-repo-cleanup

.PHONY: ensure-ecr-repo-server
ensure-ecr-repo-server:
	@echo "Checking/Creating ECR repository: $(ECR_SERVER_REPO_NAME)..."
	@aws ecr describe-repositories --repository-names $(ECR_SERVER_REPO_NAME) --region $(AWS_REGION) > /dev/null 2>&1 || \
		(echo "Repository $(ECR_SERVER_REPO_NAME) not found, creating..." && \
		 aws ecr create-repository --repository-name $(ECR_SERVER_REPO_NAME) --region $(AWS_REGION) --image-scanning-configuration scanOnPush=true > /dev/null)

.PHONY: ensure-ecr-repo-worker
ensure-ecr-repo-worker:
	@echo "Checking/Creating ECR repository: $(ECR_WORKER_REPO_NAME)..."
	@aws ecr describe-repositories --repository-names $(ECR_WORKER_REPO_NAME) --region $(AWS_REGION) > /dev/null 2>&1 || \
		(echo "Repository $(ECR_WORKER_REPO_NAME) not found, creating..." && \
		 aws ecr create-repository --repository-name $(ECR_WORKER_REPO_NAME) --region $(AWS_REGION) --image-scanning-configuration scanOnPush=true > /dev/null)

.PHONY: ensure-ecr-repo-cleanup
ensure-ecr-repo-cleanup:
	@echo "Checking/Creating ECR repository: $(ECR_CLEANUP_REPO_NAME)..."
	@aws ecr describe-repositories --repository-names $(ECR_CLEANUP_REPO_NAME) --region $(AWS_REGION) > /dev/null 2>&1 || \
		(echo "Repository $(ECR_CLEANUP_REPO_NAME) not found, creating..." && \
		 aws ecr create-repository --repository-name $(ECR_CLEANUP_REPO_NAME) --region $(AWS_REGION) --image-scanning-configuration scanOnPush=true > /dev/null)

# Push all docker images to AWS ECR
.PHONY: ecr-push
# Depends on individual push targets which now handle repo creation
ecr-push: ecr-push-server ecr-push-worker ecr-push-cleanup

# Push server docker image to AWS ECR
.PHONY: ecr-push-server
# Depends on build, login, and ensuring the repo exists
ecr-push-server: docker-build-server ecr-login ensure-ecr-repo-server
	@echo "Pushing server Docker image to ECR: $(ECR_SERVER_IMAGE)"
	@docker push $(ECR_SERVER_IMAGE)

# Push worker docker image to AWS ECR
.PHONY: ecr-push-worker
# Depends on build, login, and ensuring the repo exists
ecr-push-worker: docker-build-worker ecr-login ensure-ecr-repo-worker
	@echo "Pushing worker Docker image to ECR: $(ECR_WORKER_IMAGE)"
	@docker push $(ECR_WORKER_IMAGE)

# Push cleanup docker image to AWS ECR
.PHONY: ecr-push-cleanup
# Depends on build, login, and ensuring the repo exists
ecr-push-cleanup: docker-build-cleanup ecr-login ensure-ecr-repo-cleanup
	@echo "Pushing cleanup Docker image to ECR: $(ECR_CLEANUP_IMAGE)"
	@docker push $(ECR_CLEANUP_IMAGE)


# --- Kubernetes Deployment Targets ---

# Deploy the server application: build, push to ECR, update yaml, apply yaml
.PHONY: deploy-server
deploy-server: ecr-push-server
	@echo "Updating Kubernetes server deployment YAML $(KUBERNETES_SERVER_FILE) with image $(ECR_SERVER_IMAGE)..."
	@# Use sed to replace the image line for the 'server' container. Creates a backup file (.bak)
	@# This assumes the image line is indented and follows a 'name: server' line relatively closely.
	@# Using '#' as delimiter for sed to avoid conflicts with '/' in image names.
	@sed -i.bak '/name: server/,/image:/s#^\( *\)image: .*#\1image: $(ECR_SERVER_IMAGE)#' $(KUBERNETES_SERVER_FILE)
	@echo "Applying Kubernetes deployment configuration from $(KUBERNETES_SERVER_FILE)..."
	@kubectl apply -f $(KUBERNETES_SERVER_FILE)


# Clean up generated files and binaries
.PHONY: clean


# Help target
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  all                 Build all applications (default)"
	@echo "  build               Alias for 'all'"
	@echo "  proto               Generate gRPC code"
	@echo "  server              Build the server application"
	@echo "  worker              Build the worker application"
	@echo "  cleanup             Build the cleanup application"
	@echo "  docker-build        Build all Docker images (tags for local and ECR)"
	@echo "  docker-build-server Build the server Docker image"
	@echo "  docker-build-worker Build the worker Docker image"
	@echo "  docker-build-cleanup Build the cleanup Docker image"
	@echo "  docker-push         Push all Docker images to the local registry ($(DOCKER_REGISTRY))"
	@echo "  docker-push-server  Push the server Docker image to the local registry"
	@echo "  docker-push-worker  Push the worker Docker image to the local registry"
	@echo "  docker-push-cleanup Push the cleanup Docker image to the local registry"
	@echo "  ecr-login           Log in to AWS ECR ($(AWS_REGISTRY_URL))"
	@echo "  ensure-ecr-repos    Check/Create all necessary ECR repositories"
	@echo "  ecr-push            Push all Docker images to AWS ECR ($(AWS_REGISTRY_URL))"
	@echo "  ecr-push-server     Push the server Docker image to AWS ECR"
	@echo "  ecr-push-worker     Push the worker Docker image to AWS ECR"
	@echo "  ecr-push-cleanup    Push the cleanup Docker image to AWS ECR"
	@echo "  deploy-server       Build, push server image to ECR, update server.yml, and apply to Kubernetes"
	@echo "  run-server          Run the server application locally"
	@echo "  run-worker          Run the worker application locally"
	@echo "  run-cleanup         Run the cleanup application locally"
	@echo "  clean               Remove generated Go files and binaries"
	@echo "  docker-clean        Remove built Docker images locally (both local and ECR tags)"
	@echo "  help                Show this help message"
	@echo ""
	@echo "Configuration:"
	@echo "  DOCKER_REGISTRY     Set the default Docker registry/username (current: $(DOCKER_REGISTRY))"
	@echo "                      Example: make docker-push DOCKER_REGISTRY=myuser"
	@echo "  AWS_REGISTRY_URL    AWS ECR registry URL (current: $(AWS_REGISTRY_URL))"
	@echo "  AWS_REGION          AWS Region for ECR login (current: $(AWS_REGION))"
	@echo "  VERSION             Project version read from VERSION file (current: $(VERSION))"
