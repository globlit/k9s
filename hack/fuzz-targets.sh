#!/usr/bin/env bash
# Print the fuzz targets as a JSON list of {"pkg","target"} objects.
set -euo pipefail

cd "$(dirname "$0")/.."

first=1
printf '['
while IFS= read -r f; do
	pkg="./$(dirname "$f")"
	for t in $(grep -oE '^func Fuzz[A-Za-z0-9_]*' "$f" | cut -d' ' -f2); do
		[ "$first" -eq 1 ] || printf ','
		first=0
		printf '{"pkg":"%s","target":"%s"}' "$pkg" "$t"
	done
done < <(grep -rl --include='*_fuzz_test.go' '^func Fuzz' internal cmd 2>/dev/null | sort)
printf ']\n'
