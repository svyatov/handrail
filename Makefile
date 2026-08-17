.PHONY: test lint cover

# The same commands CI runs, so a local pass means the same thing a green check
# does. Nothing here installs a tool: golangci-lint stays out of go.mod, because
# pinning it there would put 200 modules in a go.sum whose first promise is zero
# third-party dependencies.

test:
	go test -race -shuffle=on ./...

lint:
	golangci-lint run
	golangci-lint fmt --diff

# The gate is 95, and what is left uncovered is OS failure paths whose
# reachability differs by platform (ADR 0009).
cover:
	go test -coverpkg=./... -coverprofile=cover.out ./...
	go tool cover -func=cover.out | tail -1
