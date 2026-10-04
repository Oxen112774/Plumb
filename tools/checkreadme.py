"""Verify that the captured blocks in README.md are verbatim from their files.

The README's contract is that its sample outputs are real captures, not
hand-written approximations. Each block is matched against the file it was
captured into, in order; lines containing an ellipsis are skipped because they
mark a chunk that was elided for length.

A miss does not advance the cursor: otherwise one absent line would make every
later line look absent too, and the report would bury the one line that is
actually wrong.
"""

import io
import re
import sys

# The marker is a line that appears only in this block's output, and the file is
# the capture it must match.
BLOCKS = [
    ("plumb │ prod-db-01 │ Linux", "demo.txt"),
    ("plumb · prod-db-01 · 严重", "voice.txt"),
    ("不确定的地方，每一片里都写着", "why.txt"),
    ("balance_dirty_pages · 内核等待点 · 可信度: 有依据", "why-leaf.txt"),
]

# The sample time is the one thing in a capture that is not plumb's behaviour:
# it is whatever the clock said when the block was last refreshed. Without this
# rule every re-capture fails on a date and the reader has to edit a timestamp
# by hand to get a green run — which trains them to stop running the check.
TIMESTAMP = re.compile(r"\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}")


def normalise(line):
    """Blank out wall-clock times, which differ on every run."""
    return TIMESTAMP.sub("<time>", line)


def fenced_blocks(text):
    """Return the contents of every ```-fenced block, stripped of its tag line."""
    parts = text.split("```")
    # Odd indices are inside a fence; the first line of each is the language tag.
    out = []
    for part in parts[1::2]:
        body = part.split("\n", 1)[1] if "\n" in part else ""
        out.append(body)
    return out


def check(block, lines):
    start = 0
    shown = 0
    missing = []
    for line in block.split("\n"):
        if line.startswith("$ "):
            continue  # the command line, not program output
        if "…" in line:
            continue
        if line.strip() == "":
            # Blank lines are layout the README is free to adjust, and letting an
            # empty line match would advance the cursor over a real one.
            continue
        wanted = normalise(line)
        found = None
        for i in range(start, len(lines)):
            if normalise(lines[i]) == wanted:
                found = i
                break
        if found is None:
            missing.append(line)
        else:
            shown += 1
            start = found + 1
    return shown, missing


def main():
    readme = io.open("README.md", encoding="utf-8").read()
    blocks = fenced_blocks(readme)

    failed = False
    for marker, path in BLOCKS:
        block = next((b for b in blocks if marker in b), None)
        if block is None:
            print("README has no block whose marker is %r" % marker)
            failed = True
            continue

        lines = io.open(path, encoding="utf-8").read().split("\n")
        shown, missing = check(block, lines)

        print("%s: verbatim lines matched: %d" % (path, shown))
        for line in missing:
            print("  NOT IN CAPTURE: %r" % line)
            failed = True

    sys.exit(1 if failed else 0)


main()
