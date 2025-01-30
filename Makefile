BINARY=distroface
GO=go
GOBUILD=$(GO) build
GOCLEAN=$(GO) clean
GOTEST=$(GO) test
GOGET=$(GO) get

NPM=npm
CMD_PATH=./cmd
ALL_PACKAGES=$(shell go list ./...)
WEB_DIR=./web
DEV_ROOT_DIR=/tmp/registry

.PHONY: all build test clean run deps dev build-ui install-ui init-db prod

all: clean build

# PRODUCTION BUILD
build: 
	# BUILD FRONTEND
	cd $(WEB_DIR) && $(NPM) install && $(NPM) run build
	# BUILD BACKEND
	$(GOBUILD) -o $(BINARY) $(CMD_PATH)

test:
	$(GOTEST) $(ALL_PACKAGES)

clean:
	$(GOCLEAN)
	rm -rf $(WEB_DIR)/dist $(DEV_ROOT_DIR) $(BINARY) registry.db
	find . -name ".DS_Store" -delete
	make init-db

init-db:
	sqlite3 registry.db < ./db/schema.sql
	sqlite3 registry.db < ./db/initdb.sql

# DEVELOPMENT MODE
dev: clean
	make -j 2 dev-backend dev-frontend

# DEV WITHOUT CLEAN
run-frontend:
	cd $(WEB_DIR) && $(NPM) run dev

run-backend:
	GO_ENV=development $(GO) run $(CMD_PATH)/main.go

run:
	make -j 2 run-backend run-frontend

dev-backend: init-db
	GO_ENV=development $(GO) run $(CMD_PATH)/main.go

dev-frontend:
	cd $(WEB_DIR) && $(NPM) install && $(NPM) run dev

# PRODUCTION MODE
prod: build
	GO_ENV=production ./$(BINARY)

deps:
	$(GO) mod tidy
	cd $(WEB_DIR) && $(NPM) install

format:
	gofmt -s -w .
	cd $(WEB_DIR) && $(NPM) run format
