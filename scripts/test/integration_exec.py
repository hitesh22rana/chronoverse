#!/usr/bin/env python3
"""Give each Go integration test binary an independent Testcontainers reaper."""

import os
import sys
import uuid

if len(sys.argv) < 2:
    raise SystemExit("usage: integration_exec.py TEST_BINARY [ARG ...]")

os.environ["TESTCONTAINERS_SESSION_ID"] = uuid.uuid4().hex
os.execvpe(sys.argv[1], sys.argv[1:], os.environ)
