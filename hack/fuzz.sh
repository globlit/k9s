#!/usr/bin/env bash
# Run each fuzz target in turn, one package at a time.
set -euo pipefail

FUZZTIME="${FUZZTIME:-60s}"
PARALLEL="${FUZZ_PARALLEL:-2}"

status=0
packages=$(go list ./...)
found=0
for pkg in $packages; do
	listing=$(go test -list '^Fuzz' "$pkg")
	targets=$(printf '%s\n' "$listing" | grep '^Fuzz' || true)
	for t in $targets; do
		found=$((found + 1))
		echo "==> $pkg $t"
		if ! go test -run '^$' -fuzz "^${t}\$" -fuzztime "$FUZZTIME" -parallel "$PARALLEL" "$pkg"; then
			status=1
		fi
	done
done

if [ "$found" -eq 0 ]; then
 echo "No fuzz targets discovered" >&2
 exit 1
fi
exit "$status"
