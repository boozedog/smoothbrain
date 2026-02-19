.PHONY: build dev

build:
	templ generate
	go build -o smoothbrain ./cmd/smoothbrain

dev:
	air
