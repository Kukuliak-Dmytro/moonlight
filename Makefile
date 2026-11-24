APP := moonlight
DIST := dist

.PHONY: test build build-all clean

test:
	go test -v ./...

build:
	go build -trimpath -ldflags="-s -w" -o $(DIST)/$(APP) ./cmd/moonlight

build-all:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(DIST)/$(APP)-windows-amd64.exe ./cmd/moonlight
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $(DIST)/$(APP)-darwin-arm64 ./cmd/moonlight
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(DIST)/$(APP)-darwin-amd64 ./cmd/moonlight

clean:
	rm -rf $(DIST)
