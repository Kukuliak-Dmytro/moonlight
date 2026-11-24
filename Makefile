APP := monlight
DIST := dist

.PHONY: test build build-all clean

test:
	go test -v ./...

build:
	go build -trimpath -ldflags="-s -w" -o $(DIST)/$(APP) ./cmd/monlight

build-all:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(DIST)/$(APP)-windows-amd64.exe ./cmd/monlight
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $(DIST)/$(APP)-darwin-arm64 ./cmd/monlight
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(DIST)/$(APP)-darwin-amd64 ./cmd/monlight

clean:
	rm -rf $(DIST)
