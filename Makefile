build:
	cd go && go build -o ../bin/2fado ./cmd/2fado

vet:
	cd go && go vet ./...

fmt:
	cd go && gofmt -w .
