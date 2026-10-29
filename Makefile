VERSION ?= 0.1.0-dev
LDFLAGS := -s -w -X main.version=$(VERSION)

# Binary: ./cmd/nexovirt-box (thin main), code in internal/box, UI embedded from web/dist, catalog from catalog/.

.PHONY: build ui test clean

build: ui
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/nexovirt-box-linux-amd64 ./cmd/nexovirt-box
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/nexovirt-box-linux-arm64 ./cmd/nexovirt-box

ui:
	cd web && npm ci && npm run build
	touch web/dist/.gitkeep

test:
	go vet ./...
	go test ./...

clean:
	rm -rf bin web/dist/assets web/dist/*.html web/dist/*.js web/dist/*.svg
