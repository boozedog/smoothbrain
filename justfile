VENDOR_DIR := "internal/core/web/vendor"

build:
    templ generate
    go build -o smoothbrain ./cmd/smoothbrain

dev:
    air

vendor-web:
    mkdir -p {{VENDOR_DIR}}/frankenui/js
    mkdir -p {{VENDOR_DIR}}/htmx
    curl -sL -o {{VENDOR_DIR}}/frankenui/core.min.css "https://cdn.jsdelivr.net/npm/franken-ui@2.1.2/dist/css/core.min.css"
    curl -sL -o {{VENDOR_DIR}}/frankenui/utilities.min.css "https://cdn.jsdelivr.net/npm/franken-ui@2.1.2/dist/css/utilities.min.css"
    curl -sL -o {{VENDOR_DIR}}/frankenui/js/core.iife.js "https://cdn.jsdelivr.net/npm/franken-ui@2.1.2/dist/js/core.iife.js"
    curl -sL -o {{VENDOR_DIR}}/frankenui/js/icon.iife.js "https://cdn.jsdelivr.net/npm/franken-ui@2.1.2/dist/js/icon.iife.js"
    curl -sL -o {{VENDOR_DIR}}/htmx/htmx.min.js "https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js"
    curl -sL -o {{VENDOR_DIR}}/htmx/ext-ws.js "https://unpkg.com/htmx-ext-ws@2.0.2/ws.js"
