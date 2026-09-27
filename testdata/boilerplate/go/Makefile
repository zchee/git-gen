# Fixture for internal/boilerplate.
.DEFAULT_GOAL := help

GO_PATH ?= $(shell go env GOPATH)
PACKAGE := $(subst $(GO_PATH)/src/,,$(CURDIR))

.PHONY: help
help:  ## Show make target help.
	@echo $(PACKAGE)
