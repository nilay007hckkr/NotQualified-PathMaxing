# Shortcuts for Linux, macOS and Git Bash with make installed. Every target
# is a plain command you can also run by hand.

.PHONY: run db test race vet fmt check verify

run:
	go run ./cmd/server

db:
	docker compose up -d db

test:
	go test ./...

# Needs cgo and a C compiler; CI always runs it.
race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	@out=$$(gofmt -l $$(go list -f '{{.Dir}}' ./...)); \
	if [ -n "$$out" ]; then echo "gofmt would change:"; echo "$$out"; exit 1; fi

check: vet fmt test

verify:
	bash scripts/verify-steps.sh
