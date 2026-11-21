#!/usr/bin/env bash
# Run each fuzz target in turn, one package at a time.
set -euo pipefail

FUZZTIME="${FUZZTIME:-30s}"
PARALLEL="${FUZZ_PARALLEL:-2}"

status=0
for pkg in $(go list ./...); do
	targets=$(go test -list '^Fuzz' "$pkg" | grep '^Fuzz' || true)
	for t in $targets; do
		echo "==> $pkg $t"
		if ! go test -run '^$' -fuzz "^${t}\$" -fuzztime "$FUZZTIME" -parallel "$PARALLEL" "$pkg"; then
			status=1
		fi
	done
done

exit "$status"
