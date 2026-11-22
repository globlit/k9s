#!/usr/bin/env bash
# Group fuzz targets by package so each job reuses its build cache.
set -euo pipefail
cd "$(dirname "$0")/.."
python3 - <<'PY'
import json
import pathlib
import re
packages = set()
for root in ('internal', 'cmd'):
    for path in sorted(pathlib.Path(root).rglob('*_test.go')):
        if re.search(r'^func Fuzz\w+\(f \*testing\.F\)', path.read_text(), re.M):
            packages.add(str(path.parent))
if not packages:
    raise SystemExit('No fuzz targets discovered')
print(json.dumps([{'pkg': './' + p, 'id': p.replace('/', '-')} for p in sorted(packages)]))
PY
