.PHONY: build install clean

# Binary name
BINARY=tf-os

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOGET=$(GOCMD) get

# Determine user's home directory
HOME_DIR=$(shell echo $$HOME)
LOCAL_BIN=$(HOME_DIR)/.local/bin

# Build the binary
build:
	$(GOBUILD) -o $(BINARY) -v

# Install the binary to ~/.local/bin
install: build
	mkdir -p $(LOCAL_BIN)
	cp $(BINARY) $(LOCAL_BIN)/$(BINARY)
	chmod +x $(LOCAL_BIN)/$(BINARY)
	@echo "Installed $(BINARY) to $(LOCAL_BIN)"

# Clean the binary
clean:
	$(GOCLEAN)
	rm -f $(BINARY)
	@echo "Cleaned up $(BINARY)"

# Default target
all: build
