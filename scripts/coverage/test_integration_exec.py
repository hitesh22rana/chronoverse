"""Check the integration launcher at the process boundary."""

from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import unittest
import uuid

LAUNCHER = Path(__file__).resolve().parents[1] / "test/integration_exec.py"


class IntegrationExecTests(unittest.TestCase):
    def launch(self, code, *args, **kwargs):
        return subprocess.Popen([sys.executable, str(LAUNCHER), sys.executable, "-c", code, *args],
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, **kwargs)

    def test_concurrent_children_have_distinct_sessions_and_preserve_arguments(self):
        code = ("import json,os,sys; print(json.dumps([os.environ['TESTCONTAINERS_SESSION_ID'], "
                "os.environ['UNCHANGED'],sys.argv[1:],os.getpid()]))")
        def run(_):
            child = self.launch(code, "space in argument", "--flag=value", "", env={
                **os.environ, "TESTCONTAINERS_SESSION_ID": "shared-parent", "UNCHANGED": "preserved"})
            stdout, stderr = child.communicate(timeout=10)
            self.assertEqual(child.returncode, 0, stderr)
            session, preserved, args, pid = json.loads(stdout)
            self.assertEqual(uuid.UUID(hex=session).version, 4)
            self.assertEqual(preserved, "preserved")
            self.assertEqual(args, ["space in argument", "--flag=value", ""])
            self.assertEqual(pid, child.pid)
            return session
        with ThreadPoolExecutor(max_workers=4) as pool:
            sessions = list(pool.map(run, range(12)))
        self.assertEqual(len(set(sessions)), 12)

    def test_exit_status_and_standard_streams_are_preserved(self):
        child = self.launch("import sys; print(sys.stdin.read()); print('error',file=sys.stderr); sys.exit(23)",
                            stdin=subprocess.PIPE)
        stdout, stderr = child.communicate("input text", timeout=10)
        self.assertEqual(child.returncode, 23)
        self.assertEqual(stdout, "input text\n")
        self.assertEqual(stderr, "error\n")

    @unittest.skipUnless(os.name == "posix", "POSIX signal semantics")
    def test_signal_reaches_the_execed_binary(self):
        child = self.launch("import time; print('ready',flush=True); time.sleep(60)")
        self.addCleanup(lambda: child.poll() is None and child.kill())
        self.assertEqual(child.stdout.readline().strip(), "ready")
        child.send_signal(signal.SIGTERM)
        child.communicate(timeout=10)
        self.assertEqual(child.returncode, -signal.SIGTERM)

    def test_missing_binary_fails(self):
        result = subprocess.run([sys.executable, str(LAUNCHER)], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("usage:", result.stderr)


if __name__ == "__main__":
    unittest.main()
