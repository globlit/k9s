#!/usr/bin/env python3
"""Gate significant benchmark regressions and reject missing comparison data."""
import csv
import sys
import re
import math

LIMITS = {"sec/op": 10.0, "B/op": 5.0, "allocs/op": 5.0}


def parse(lines):
    pkg, unit, out = "", "", []
    for row in csv.reader(lines):
        if not row:
            continue
        if row[0].startswith("pkg: "):
            pkg = row[0][5:]
            continue
        if len(row) > 2 and row[0] == "" and row[2] == "CI":
            unit = row[1] if row[1] in LIMITS else ""
            continue
        if not unit or row[0] in ("", "geomean") or len(row) == 1:
            continue
        if len(row) < 7 or not row[1] or not row[3]:
            raise ValueError(f"Missing comparison: {pkg} {row[0]} {unit}")
        base, head = float(row[1]), float(row[3])
        if not all(math.isfinite(x) and x >= 0 for x in (base, head)):
            raise ValueError("Invalid benchmark measurement")
        change = row[5].strip()
        if change == "~":
            delta = 0.0
        elif change.endswith("%"):
            delta = float(change[:-1])
        elif base == 0 and head > 0:
            delta = float("inf")
        else:
            raise ValueError(f"Invalid comparison: {row}")
        stats = re.fullmatch(r"p=([0-9.]+) n=(\d+)(?:\+(\d+))?", row[6])
        if not stats or min(int(stats[2]), int(stats[3] or stats[2])) < 10:
            raise ValueError(f"Missing statistics or fewer than ten samples: {row}")
        if change != "~" and float(stats[1]) > 0.05:
            raise ValueError(f"Inconsistent significance: {row}")
        if math.isnan(delta):
            raise ValueError("Invalid delta")
        out.append((pkg, row[0], unit, row[1], row[3], delta))
    if not out:
        raise ValueError("No comparable benchmarks")
    return out


def main():
    if len(sys.argv) != 2:
        sys.exit("usage: benchcheck.py STAT.csv")
    try:
        with open(sys.argv[1], newline="") as f:
            rows = parse(f)
    except (ValueError, OSError) as err:
        print(f"Benchmark check failed: {err}")
        return 1
    bad = [r for r in rows if r[5] > LIMITS[r[2]]]
    print("## Benchmark check\n")
    print("Limits: time 10%; bytes and allocations 5%. Only significant changes gate.\n")
    table(rows)
    print(f"{len(bad)} regressions; {len(rows)} comparisons.")
    return int(bool(bad))


def table(rows):
    print("| package | benchmark | unit | base | head | change |")
    print("|---|---|---|---|---|---|")
    for pkg, name, unit, base, head, change in rows:
        print(f"| {pkg} | {name} | {unit} | {base} | {head} | {change:+.2f}% |")
    print()


if __name__ == "__main__":
    sys.exit(main())
