#!/usr/bin/env bash
# Discover fuzz functions in every Go test file, not just *_fuzz_test.go.
set -euo pipefail
cd "$(dirname "$0")/.."
python3 - <<'PY'
import json
import pathlib
import re
out = []
for root in ('internal', 'cmd'):
    for path in sorted(pathlib.Path(root).rglob('*_test.go')):
        for target in re.findall(r'^func (Fuzz\w+)\(f \*testing\.F\)', path.read_text(), re.M):
            out.append({'pkg': './' + str(path.parent), 'target': target})
if not out:
    raise SystemExit('No fuzz targets discovered')
print(json.dumps(out))
PY
