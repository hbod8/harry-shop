dev:
	go run . serve

seed:
	go run . seed

# PocketBase must be running: the catalog is read from its API.
# --cleanDestinationDir removes pages for products that became unlisted.
site:
	hugo --source site --cleanDestinationDir

test:
	go test ./...

build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o harry-shop .

.PHONY: dev seed site test build
