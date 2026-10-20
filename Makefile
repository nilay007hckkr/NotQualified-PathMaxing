# Shortcuts for Linux, macOS and Git Bash with make installed. Every target
# is a plain command you can also run by hand.

.PHONY: run db test race vet fmt check verify web seed docker

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

web:
	cd web && npm ci && npm run build && npm test

# Demo traffic through the real API and redirects (see cmd/seed).
seed:
	go run ./cmd/seed -base http://localhost:8080 -password "$${SEED_PASSWORD}"

docker:
	docker build -t pathmaxing-server .
