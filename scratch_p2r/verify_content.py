#!/usr/bin/env python3
"""Content-equivalence check for the Phase 2R build: the multiset of
significant lines (non-blank, non-comment) of each new 9xx file must equal
the multiset from its sources at git HEAD, after the two declared
transforms (CONCURRENTLY stripping, 957-down regeneration)."""
import os
import re
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MIG = "server/migrations"
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from build import GROUPS, strip_concurrently, MIG as MIGDIR, REGEN_957_DOWN  # noqa: E402


def sig_lines(text):
    out = []
    for ln in text.splitlines():
        s = ln.strip()
        if s and not s.startswith("--"):
            out.append(s)
    return out


def head_file(stem, direction):
    return subprocess.run(
        ["git", "-C", ROOT, "show", f"HEAD:{MIG}/{stem}.{direction}.sql"],
        capture_output=True, text=True, check=True,
    ).stdout


def main():
    failures = 0
    for new, sources in GROUPS:
        for direction in ("up", "down"):
            with open(os.path.join(MIGDIR, f"{new}.{direction}.sql"), encoding="utf-8") as f:
                got = sig_lines(f.read())
            want = []
            order = sources if direction == "up" else list(reversed(sources))
            for src in order:
                if src == "957_legislation_e1_removal" and direction == "down":
                    with open(REGEN_957_DOWN, encoding="utf-8") as f:
                        regen = f.read().split("\n", 1)[1]
                    want.extend(sig_lines(regen))
                else:
                    want.extend(sig_lines(strip_concurrently(head_file(src, direction))))
            if got != want:
                failures += 1
                from collections import Counter
                cg, cw = Counter(got), Counter(want)
                only_new = list((cg - cw).elements())[:8]
                only_old = list((cw - cg).elements())[:8]
                print(f"DIFF {new}.{direction}.sql: new-only={only_new} old-only={only_old}")
            else:
                print(f"OK   {new}.{direction}.sql ({len(got)} significant lines)")
    if failures:
        sys.exit(f"FATAL {failures} file(s) differ")
    print("---- all files equivalent ----")


if __name__ == "__main__":
    main()
