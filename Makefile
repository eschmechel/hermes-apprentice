.PHONY: test-go test-python test coverage lint fmt

GO_MODS := proxy dataset-builder registry-service burst
PY_DIRS := orchestrator trainer validator serving telegram

ROOT := $(shell pwd)

test-go:
	@for mod in $(GO_MODS); do \
		echo "=== $$mod ==="; \
		(cd $(ROOT)/$$mod && go test ./...); \
	done

PYTEST := /tmp/apprentice-test-venv/bin/python -m pytest

test-python:
	@cd $(ROOT)/orchestrator && $(PYTEST) tests/ -q
	@cd $(ROOT)/trainer && $(PYTEST) tests/ -q
	@cd $(ROOT)/validator && $(PYTEST) tests/ -q
	@cd $(ROOT)/serving && $(PYTEST) tests/ -q
	@cd $(ROOT)/telegram && $(PYTEST) tests/ -q

test: test-go test-python

coverage-go:
	@for mod in $(GO_MODS); do \
		echo "=== $$mod ==="; \
		(cd $(ROOT)/$$mod && go test -coverprofile=/tmp/cov-$$mod.out ./... && \
		go tool cover -func=/tmp/cov-$$mod.out | tail -1); \
	done

coverage: coverage-go
	@echo "=== Python coverage ==="
	@cd orchestrator && python -m pytest tests/ -q --cov=src/apprentice_orchestrator --cov-report=term-missing
	@cd trainer && python -m pytest tests/ -q --cov=src/apprentice_trainer --cov-report=term-missing
	@cd validator && python -m pytest tests/ -q --cov=src/apprentice_validator --cov-report=term-missing

lint-go:
	@for mod in $(GO_MODS); do \
		(cd $(ROOT)/$$mod && go vet ./...); \
	done

lint: lint-go

fmt-go:
	@for mod in $(GO_MODS); do \
		(cd $(ROOT)/$$mod && gofmt -w .); \
	done
