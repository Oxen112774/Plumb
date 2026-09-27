"""Verify that the demo block in README.md is verbatim from demo.txt.

The README's contract is that its sample output is a real capture, not a
hand-written approximation. This walks both files in order and reports any
README line that does not appear verbatim in the capture. Lines containing an
ellipsis are skipped: they mark a chunk that was elided for length.
"""

import io
import sys

readme = io.open("README.md", encoding="utf-8").read()
real = io.open("demo.txt", encoding="utf-8").read().split("\n")

demo = None
for block in readme.split("```"):
    if "plumb · 服务器卡顿归因" in block:
        demo = block.split("\n", 1)[1]  # drop the leading blank line
        break
if demo is None:
    sys.exit("README has no demo output block")

start = 0
shown = 0
missing = []
for line in demo.split("\n"):
    if line == "$ plumb":
        continue  # the command line, not program output
    if "…" in line:
        continue
    if line.strip() == "":
        # Blank lines are layout the README is free to adjust, and letting an
        # empty line match would advance the cursor over a real one.
        continue
    found = None
    for i in range(start, len(real)):
        if real[i] == line:
            found = i
            break
    if found is None:
        # Do not advance the cursor on a miss, otherwise one absent line makes
        # every later line look absent too.
        missing.append(line)
    else:
        shown += 1
        start = found + 1

print("verbatim lines matched: %d" % shown)
for line in missing:
    print("NOT IN CAPTURE: %r" % line)
sys.exit(1 if missing else 0)
