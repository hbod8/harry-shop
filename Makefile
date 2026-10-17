dev:
	go run . serve

seed:
	go run . seed

site:
	hugo --source site

test:
	go test ./...

build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o harry-shop .

.PHONY: dev seed site test build
