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

    def test_exec_boundary_receives_fresh_session_and_preserved_process_inputs(self):
        import runpy
        from unittest import mock
        args = [str(LAUNCHER), "/real/test binary", "space in argument", "--flag=value", ""]
        inherited = {"TESTCONTAINERS_SESSION_ID": "shared-parent", "UNCHANGED": "preserved"}
        with mock.patch.dict(os.environ, inherited, clear=True), mock.patch.object(sys, "argv", args):
            with mock.patch.object(os, "execvpe") as execute:
                runpy.run_path(str(LAUNCHER), run_name="__main__")
                execute.assert_called_once()
                executable, argv, environment = execute.call_args.args
                self.assertEqual(executable, args[1])
                self.assertEqual(argv, args[1:])
                self.assertEqual(environment["UNCHANGED"], "preserved")
                session = environment["TESTCONTAINERS_SESSION_ID"]
                self.assertEqual(len(session), 32)
                self.assertEqual(uuid.UUID(hex=session).version, 4)
                self.assertNotEqual(session, inherited["TESTCONTAINERS_SESSION_ID"])

    def test_no_binary_exits_before_replacing_process_or_environment(self):
        import runpy
        from unittest import mock
        with mock.patch.dict(os.environ, {"TESTCONTAINERS_SESSION_ID": "parent"}, clear=True):
            with mock.patch.object(sys, "argv", [str(LAUNCHER)]), mock.patch.object(os, "execvpe") as execute:
                with self.assertRaises(SystemExit) as result:
                    runpy.run_path(str(LAUNCHER), run_name="__main__")
                self.assertEqual(str(result.exception), "usage: integration_exec.py TEST_BINARY [ARG ...]")
                execute.assert_not_called()
                self.assertEqual(dict(os.environ), {"TESTCONTAINERS_SESSION_ID": "parent"})

    def test_missing_binary_fails(self):
        result = subprocess.run([sys.executable, str(LAUNCHER)], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("usage:", result.stderr)


if __name__ == "__main__":
    unittest.main()
