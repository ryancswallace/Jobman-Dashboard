#!/usr/bin/env python3
"""Allow only an explicit registry manifest-not-found response before first push."""
import re
import subprocess
import sys


def absent(result):
    return result.returncode != 0 and bool(re.search(r"(?:manifest unknown|: not found)\s*$", result.stderr.strip(), re.IGNORECASE))


def main():
    if len(sys.argv) != 2 or not re.fullmatch(r"ghcr\.io/[a-z0-9_.-]+/[a-z0-9_.-]+:v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)-rc\.[1-9][0-9]*", sys.argv[1]):
        raise SystemExit("Expected a fully qualified versioned RC image")
    result = subprocess.run(["docker", "buildx", "imagetools", "inspect", sys.argv[1]], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=120)
    if not absent(result):
        raise SystemExit("Image exists or registry absence could not be established; use a new RC or repair registry access")


if __name__ == "__main__":
    main()
