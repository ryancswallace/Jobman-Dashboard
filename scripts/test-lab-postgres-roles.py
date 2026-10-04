#!/usr/bin/env python3
"""Run disposable-schema/password-login role tests with the synthetic Lab admin.

The test creates randomly named temporary roles and drops their owned privileges.
It never changes existing roles, public grants, live schemas or HBA configuration.
All credentials remain in the subprocess environment; output is sanitized.
"""
import os
from pathlib import Path
import subprocess
import sys
from urllib.parse import quote

root = Path(__file__).resolve().parents[1]
lab = Path(os.environ.get("JOBMAN_LAB_ROOT", str(root.parent / "jobman-lab")))
password = None
for line in (lab / ".lab/credentials/lab.env").read_text().splitlines():
    name, sep, value = line.partition("=")
    if sep and name == "JOBMAN_LAB_POSTGRES_PASSWORD":
        password = value.strip().strip("'\"")
if not password:
    sys.exit("Synthetic Lab PostgreSQL bootstrap key is unavailable.")
ca = quote(str(lab / ".lab/certs/lab-ca.crt"), safe="")
url = f"postgres://jobman_control:{quote(password, safe='')}@10.77.0.20:5432/jobman_dashboard?sslmode=verify-full&sslrootcert={ca}"
env = os.environ.copy()
env.pop("GOROOT", None)
env.update(JOBMAN_DASHBOARD_TEST_DATABASE_URL=url, GOTOOLCHAIN="go1.26.6", GOWORK="off")
env.setdefault("GOCACHE", "/private/tmp/jobman-dashboard-go-cache" if sys.platform == "darwin" else "/tmp/jobman-dashboard-go-cache")
result = subprocess.run(["go", "test", "-race", "-count=1", "-v", "./internal/store", "-run", "^TestRuntimeRoles", "-timeout", "3m"], cwd=root, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
print(result.stdout.replace(url, "[REDACTED_LAB_DATABASE]").replace(password, "[REDACTED]"), end="")
sys.exit(result.returncode)
