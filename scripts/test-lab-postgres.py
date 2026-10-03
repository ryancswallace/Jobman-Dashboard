#!/usr/bin/env python3
"""Run isolated Dashboard DB tests using only the authorized synthetic Lab key.

The database tests create random temporary schemas and remove only those schemas.
No credential value or connection URL is emitted, including on test failure.
"""
import os
from pathlib import Path
import subprocess
import sys
from urllib.parse import quote

root = Path(__file__).resolve().parents[1]
lab = Path(os.environ.get("JOBMAN_LAB_ROOT", str(root.parent / "jobman-lab")))
key = "JOBMAN_LAB_POSTGRES_PASSWORD"
password = None
for line in (lab / ".lab/credentials/lab.env").read_text().splitlines():
    name, sep, value = line.partition("=")
    if sep and name == key:
        password = value.strip().strip("'\"")
if not password:
    sys.exit("Synthetic Lab PostgreSQL key is unavailable.")
url = f"postgres://jobman_control:{quote(password, safe='')}@10.77.0.20:5432/jobman_control?sslmode=disable"
env = os.environ.copy()
env.pop("GOROOT", None)
env.update(JOBMAN_DASHBOARD_TEST_DATABASE_URL=url, GOTOOLCHAIN="go1.26.6")
env.setdefault("GOCACHE", "/private/tmp/jobman-dashboard-go-cache" if sys.platform == "darwin" else "/tmp/jobman-dashboard-go-cache")
result = subprocess.run(["go", "test", "-race", "-count=1", "-v", "./internal/store"], cwd=root, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
output = result.stdout.replace(url, "[REDACTED_LAB_DATABASE]").replace(password, "[REDACTED]")
print(output, end="")
sys.exit(result.returncode)
