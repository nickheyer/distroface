# DISTROFACE MAKE
# RUN 'make dev' FOR LOCAL INSTANCE
# RUN 'make prod' FOR LOCAL PROD INSTANCE W/O VITE PROXY
# RUN 'make clean' TO DELETE FILES

SHELL := /bin/sh
BUILD_DIR ?= ./build
BUILD_DIR_DF ?= $(BUILD_DIR)/distroface
BUILD_DIR_DFCLI ?= $(BUILD_DIR)/dfcli
BINARY        ?= $(BUILD_DIR_DF)/distroface
CLI_BINARY    ?= $(BUILD_DIR_DFCLI)/dfcli
CMD_PATH      ?= ./cmd/distroface
CLI_CMD_PATH  ?= ./cmd/dfcli
WEB_DIR       ?= ./web
CONFIG_FILE   ?= config.yml

# TOOLS
GO            ?= go
NPM           ?= npm
YQ            ?= yq  # YQ (YAML FLAVORED JQ - NEEDS TO BE INSTALLED)

# DB PATH IS PARSED FROM CONFIG.YML W/ YQ
STORAGE_ROOT  := $(shell $(YQ) -r '.storage.root_directory' $(CONFIG_FILE) 2>/dev/null || echo registry)
DB_PATH       := $(shell $(YQ) -r '.database.path' $(CONFIG_FILE) 2>/dev/null || echo registry.db)
GOBUILD       = $(GO) build
GOCLEAN       = $(GO) clean
GOTEST        = $(GO) test
ALL_PACKAGES  = $(shell go list ./...)

.PHONY: all build test clean dev run dev-backend dev-frontend deps format prod build-cli
all: build

## -----------------------
## PROD
## -----------------------
build: build-cli
	rm -rf $(BINARY)
	mkdir -p $(BUILD_DIR_DF)
	@echo "Building web frontend..."
	cd $(WEB_DIR) && $(NPM) install && $(NPM) run build
	@echo "Building Go backend..."
	$(GOBUILD) -o $(BINARY) $(CMD_PATH)

prod: build
	@echo "Starting $(BINARY) in production mode..."
	GO_ENV=production ./$(BINARY)

## -----------------------
## TEST, LINT, AND FORMATTING
## -----------------------
test:
	$(GOTEST) $(ALL_PACKAGES)

format:
	gofmt -s -w .
	cd $(WEB_DIR) && $(NPM) run format

## -----------------------
## CLEANING (FOR DEV)
## -----------------------
clean:
	@echo "Cleaning build artifacts..."
	$(GOCLEAN)
	rm -rf $(WEB_DIR)/dist $(BINARY) $(STORAGE_ROOT) $(DB_PATH) $(BUILD_DIR)
	find . -name ".DS_Store" -delete 


## -----------------------
## DEV
## -----------------------
dev: clean
	@echo "Starting dev mode (frontend + backend in parallel)..."
	$(MAKE) -j 2 dev-backend dev-frontend

run:
	@echo "Running backend + frontend at once..."
	$(MAKE) -j 2 run-backend run-frontend

dev-backend:
	@echo "Starting backend in development mode with DB_PATH=$(DB_PATH)..."
	GO_ENV=development $(GO) run $(CMD_PATH)/main.go

dev-frontend:
	@echo "Starting frontend (SvelteKit dev server)..."
	cd $(WEB_DIR) && $(NPM) install && $(NPM) run dev

dev-cli:
	@echo "Building and watching CLI..."
	$(GO) run $(CLI_CMD_PATH)/main.go

run-backend:
	@echo "Running backend with existing DB (no init)..."
	GO_ENV=development $(GO) run $(CMD_PATH)/main.go

run-frontend:
	cd $(WEB_DIR) && $(NPM) run dev

## -----------------------
## DEPENDENCIES
## -----------------------
build-cli:
	@echo "Building CLI..."
	rm -rf $(CLI_BINARY)
	mkdir -p $(BUILD_DIR_DFCLI)
	CGO_ENABLED=0 $(GOBUILD) -o $(CLI_BINARY) $(CLI_CMD_PATH)

deps: build-cli
	@echo "Tidying Go modules and installing NPM modules..."
	$(GO) mod tidy
	cd $(WEB_DIR) && $(NPM) install

