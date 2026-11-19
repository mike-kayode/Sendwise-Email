#!/usr/bin/env python3
import re
import sys
from pathlib import Path


KEY = re.compile(r'^(\s*)([A-Za-z0-9_.-]+)\s*:')


def check(path: Path) -> list[str]:
    duplicates: list[str] = []
    seen: dict[tuple[tuple[str, ...], str], int] = {}
    stack: list[tuple[int, str]] = []
    for number, line in enumerate(path.read_text().splitlines(), 1):
        match = KEY.match(line)
        if not match:
            continue
        indent = len(match.group(1).expandtabs(2))
        key = match.group(2)
        while stack and stack[-1][0] >= indent:
            stack.pop()
        parent = tuple(item[1] for item in stack)
        mapping = (parent, key)
        if mapping in seen:
            duplicates.append(
                f"{path}:{number}: duplicate YAML key {key!r} in "
                f"{'/'.join(parent) if parent else '<root>'}"
            )
        else:
            seen[mapping] = number
        stack.append((indent, key))
    return duplicates


root = Path(__file__).resolve().parent.parent
errors: list[str] = []
for relative in ("docker-compose.yml", "docker-compose.microservices.yml"):
    errors.extend(check(root / relative))
if errors:
    print("Duplicate YAML keys found:", file=sys.stderr)
    print("\n".join(errors), file=sys.stderr)
    sys.exit(1)
print("Compose YAML key scan passed.")
