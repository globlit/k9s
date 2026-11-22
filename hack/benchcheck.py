#!/usr/bin/env python3
"""Check benchstat CSV output (benchstat -format csv base head).

Fail when B/op or allocs/op regress by more than the threshold. Report
sec/op changes as advice only. Write a Markdown report to stdout.
"""
import csv
import sys

GATED = {"B/op", "allocs/op"}
ADVISORY = {"sec/op"}


def pct(s):
    s = s.strip()
    if not s.endswith("%") or s[0] not in "+-":
        return None
    try:
        return float(s[:-1])
    except ValueError:
        return None


def parse(lines):
    pkg, unit, out = "", "", []
    for row in csv.reader(lines):
        if not row:
            continue
        if len(row) == 1 and row[0].startswith("pkg: "):
            pkg = row[0][5:]
            continue
        if len(row) > 1 and row[0] == "" and row[1] and "/" in row[1]:
            unit = row[1]
            continue
        if row[0] in ("", "geomean") or len(row) < 7:
            continue
        change = pct(row[5])
        if change is not None:
            out.append((pkg, row[0], unit, row[1], row[3], change))
    return out


def main():
    if len(sys.argv) not in (2, 3):
        sys.exit("usage: benchcheck.py STAT.csv [THRESHOLD_PCT]")
    limit = float(sys.argv[2]) if len(sys.argv) == 3 else 10.0
    with open(sys.argv[1], newline="") as f:
        rows = parse(f)

    bad = [r for r in rows if r[2] in GATED and r[5] > limit]
    slow = [r for r in rows if r[2] in ADVISORY and abs(r[5]) > limit]

    print("## Benchmark check\n")
    print(f"Fail on a B/op or allocs/op regression above {limit:g}%.\n")
    if bad:
        print("### Memory regressions (fail)\n")
        table(bad)
    else:
        print("No memory regression.\n")
    if slow:
        print("### Time changes (advice only, shared runners are noisy)\n")
        table(slow)
    sys.exit(1 if bad else 0)


def table(rows):
    print("| package | benchmark | unit | base | head | change |")
    print("|---|---|---|---|---|---|")
    for pkg, name, unit, base, head, change in rows:
        print(f"| {pkg} | {name} | {unit} | {base} | {head} | {change:+.2f}% |")
    print()


if __name__ == "__main__":
    main()
