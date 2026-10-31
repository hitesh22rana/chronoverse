"""Provenance, trust boundaries and idempotent PR publishing tests."""

import hashlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import urllib.error
import warnings
import zipfile

import ci_report as report

BASE = "a" * 40
HEAD = "b" * 40
MERGE = "c" * 40
SCORER = "d" * 40
REPO = "owner/project"
INVENTORY = b"path,start_line,end_line,language,namespace,name,complexity,coverage,coverage_recorded,crap\napp.go,1,3,go,pkg,Fn,2,100,yes,2\n"


def metadata(**changes):
    data = {"schema_version": 1, "scope_version": 1, "scorer_revision": SCORER,
            "reporter_sha256": "f" * 64, "repository": REPO, "event_name": "push", "head_sha": BASE,
            "base_sha": None, "tested_sha": BASE, "run_id": "12",
            "inventory_sha256": hashlib.sha256(INVENTORY).hexdigest()}
    data.update(changes)
    return data


def archive(meta=None, entries=()):
    output = io.BytesIO()
    with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED) as handle:
        handle.writestr(".metrics/inventory.csv", INVENTORY)
        if meta is not False:
            handle.writestr(".metrics/report-meta.json", json.dumps(meta or metadata()))
        for path, data in entries:
            handle.writestr(path, data)
    return output.getvalue()


def event():
    return {"number": 42, "pull_request": {"head": {"sha": HEAD, "repo": {"full_name": REPO}},
                                           "base": {"sha": BASE}}}


class FakeAPI:
    repository = REPO

    def __init__(self, comments=(), live_head=HEAD, live_base=BASE):
        self.comments = list(comments)
        self.live_head = live_head
        self.live_base = live_base
        self.requests = []

    def request(self, path, method="GET", data=None):
        self.requests.append((path, method, data))
        if method == "GET":
            return {"state": "open", "head": {"sha": self.live_head}, "base": {"sha": self.live_base}}
        return {"id": 57}

    def pages(self, path, key=None):
        if "comments" in path:
            return iter(self.comments)
        if "workflow_runs" == key:
            return iter([{"id": 8, "event": "push", "head_sha": BASE, "head_branch": "main",
                          "head_repository": {"full_name": REPO}, "conclusion": "failure",
                          "html_url": "https://github.com/owner/project/actions/runs/8"}])
        if key == "artifacts":
            return iter([{"id": 3, "name": "crap-report", "expired": False}])
        if key == "jobs":
            return iter([{"name": "CRAP report", "status": "completed", "conclusion": "success"},
                         {"name": "Release server", "status": "completed", "conclusion": "failure"}])
        raise AssertionError(path)

    def download_artifact(self, artifact_id):
        return archive(metadata(run_id="8"))


class ArchiveTests(unittest.TestCase):
    def test_accepts_matching_provenance_and_ignores_other_report_files(self):
        data = archive(entries=[("target/coverage/go/coverage.out", "irrelevant")])
        self.assertEqual(report.read_baseline_archive(data, metadata(), BASE), INVENTORY)

    def test_rejects_each_provenance_mismatch(self):
        cases = {"schema_version": 2, "scope_version": 2, "scorer_revision": "e" * 40,
                 "reporter_sha256": "0" * 64, "repository": "other/repository", "event_name": "pull_request",
                 "head_sha": HEAD, "tested_sha": MERGE, "inventory_sha256": "0" * 64}
        for field, value in cases.items():
            with self.subTest(field=field), self.assertRaises(report.ReportError):
                report.read_baseline_archive(archive(metadata(**{field: value})), metadata(), BASE)

    def test_first_rollout_without_metadata_is_not_a_zero_baseline(self):
        with self.assertRaisesRegex(report.ReportError, "first rollout"):
            report.read_baseline_archive(archive(False), metadata(), BASE)

    def test_rejects_duplicate_or_ambiguous_inventory(self):
        for path in (".metrics/inventory.csv", "inventory.csv"):
            with self.subTest(path=path), warnings.catch_warnings():
                warnings.simplefilter("ignore", UserWarning)
                data = archive(entries=[(path, INVENTORY)])
            with self.assertRaisesRegex(report.ReportError, "ambiguous"):
                report.read_baseline_archive(data, metadata(), BASE)

    def test_rejects_traversal_without_extracting_archive(self):
        for path in ("../outside", "/absolute", "safe/../../outside", "safe\\outside"):
            with self.subTest(path=path), self.assertRaisesRegex(report.ReportError, "unsafe"):
                report.read_baseline_archive(archive(entries=[(path, "ignored")]), metadata(), BASE)

    def test_limits_decompressed_entry_size(self):
        data = archive(metadata(extra="x" * 70000))
        with self.assertRaisesRegex(report.ReportError, "too large"):
            report.read_baseline_archive(data, metadata(), BASE)

    def test_run_identity_must_match_the_artifact_owner(self):
        with self.assertRaisesRegex(report.ReportError, "run identity"):
            report.read_baseline_archive(archive(), metadata(), BASE, 999)

    def test_exact_base_report_can_survive_unrelated_release_failure(self):
        inventory, status = report.find_baseline(FakeAPI(), metadata(base_sha=BASE))
        self.assertEqual(inventory, INVENTORY)
        self.assertTrue(status["available"])
        self.assertEqual(status["run_id"], 8)

    def test_report_job_must_have_succeeded(self):
        api = FakeAPI()
        original = api.pages
        api.pages = lambda path, key=None: iter([{"name": "CRAP report", "status": "completed", "conclusion": "failure"}]) if key == "jobs" else original(path, key)
        inventory, status = report.find_baseline(api, metadata(base_sha=BASE))
        self.assertIsNone(inventory)
        self.assertFalse(status["available"])
        self.assertIn("has not succeeded", status["reason"])

    def test_old_or_foreign_run_cannot_supply_baseline(self):
        for changed in ({"head_sha": HEAD}, {"head_branch": "feature"}, {"event": "pull_request"},
                        {"head_repository": {"full_name": "attacker/fork"}}):
            api = FakeAPI()
            original = api.pages
            def pages(path, key=None):
                values = list(original(path, key))
                if key == "workflow_runs":
                    values[0].update(changed)
                return iter(values)
            api.pages = pages
            with self.subTest(changed=changed):
                inventory, status = report.find_baseline(api, metadata(base_sha=BASE))
                self.assertIsNone(inventory)
                self.assertFalse(status["available"])

    def test_baseline_failure_removes_stale_csv_and_reports_unavailable(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            report.write_json(root / ".metrics/report-meta.json", metadata(base_sha=BASE, event_name="pull_request"))
            stale = root / ".metrics/base-inventory.csv"
            stale.write_text("old")
            api = FakeAPI()
            api.download_artifact = mock.Mock(side_effect=urllib.error.HTTPError("https://api.github.com/x", 503, "unavailable", {}, None))
            with mock.patch("builtins.print"):
                result = report.baseline(root, api)
            self.assertFalse(result["available"])
            self.assertFalse(stale.exists())
            self.assertIn("HTTP 503", result["reason"])
            self.assertEqual(json.loads((root / ".metrics/baseline-status.json").read_text()), result)


class HTTPTests(unittest.TestCase):
    def test_artifact_redirect_uses_unsigned_fresh_request(self):
        api = report.GitHub(REPO, "secret-api-token")
        error = urllib.error.HTTPError("https://api.github.com/artifact", 302, "redirect",
                                      {"Location": "https://blob.example/archive?signature=temporary"}, None)
        response = mock.MagicMock()
        response.read.return_value = b"zip"
        api.opener.open = mock.Mock(side_effect=[error, response])
        self.assertEqual(api.download_artifact(4), b"zip")
        signed_request = api.opener.open.call_args_list[0].args[0]
        self.assertEqual(signed_request.get_header("Authorization"), "Bearer secret-api-token")
        unsigned_request = api.opener.open.call_args_list[1].args[0]
        self.assertIsNone(unsigned_request.get_header("Authorization"))
        self.assertEqual(unsigned_request.full_url, "https://blob.example/archive?signature=temporary")

    def test_rejects_insecure_or_credentialed_redirect(self):
        for location in ("http://blob.example/archive", "https://user:password@blob.example/archive"):
            api = report.GitHub(REPO, "token")
            api.opener.open = mock.Mock(side_effect=urllib.error.HTTPError(
                "https://api.github.com/a", 302, "redirect", {"Location": location}, None))
            with self.subTest(location=location), mock.patch("urllib.request.urlopen") as download:
                with self.assertRaises(report.ReportError):
                    api.download_artifact(1)
                download.assert_not_called()

    def test_later_blob_redirect_cannot_downgrade_https(self):
        api = report.GitHub(REPO, "secret")
        api.opener.open = mock.Mock(side_effect=[
            urllib.error.HTTPError("https://api.github.com/a", 302, "redirect",
                                   {"Location": "https://blob.example/a"}, None),
            urllib.error.HTTPError("https://blob.example/a", 302, "redirect",
                                   {"Location": "http://blob.example/b"}, None),
        ])
        with self.assertRaises(report.ReportError):
            api.download_artifact(1)
        self.assertEqual(api.opener.open.call_count, 2)
        self.assertIsNone(api.opener.open.call_args.args[0].get_header("Authorization"))

    def test_bounded_download_rejects_oversize_response(self):
        with self.assertRaises(report.ReportError):
            report.bounded_read(io.BytesIO(b"12345"), 4)

    def test_pages_follow_later_comments(self):
        api = report.GitHub(REPO, "token")
        api.request = mock.Mock(side_effect=[[{"id": i} for i in range(100)], [{"id": 100}]])
        self.assertEqual(len(list(api.pages("/comments"))), 101)
        self.assertIn("page=2", api.request.call_args.args[0])


class PublishingTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.env = {"GITHUB_EVENT_NAME": "pull_request", "GITHUB_SERVER_URL": "https://github.com"}
        report.write_json(self.root / ".metrics/report-meta.json", metadata(
            event_name="pull_request", base_sha=BASE, head_sha=HEAD, tested_sha=MERGE, pr_number=42))
        self.summary = self.root / "summary.md"
        self.summary.write_text("## Coverage and CRAP\n\nMeasured results.\n")

    def test_updates_only_own_marker_and_preserves_other_comments(self):
        comments = [
            {"id": 1, "body": report.MARKER + " author notes", "user": {"login": "author", "type": "User"}},
            {"id": 2, "body": "Unrelated bot text", "user": {"login": "github-actions[bot]", "type": "Bot"}},
            {"id": 3, "body": report.MARKER + " previous report", "user": {"login": "github-actions[bot]", "type": "Bot"}},
        ]
        api = FakeAPI(comments)
        result = report.publish(self.root, self.summary, api, event(), self.env)
        self.assertTrue(result["published"])
        writes = [entry for entry in api.requests if entry[1] != "GET"]
        self.assertEqual(len(writes), 1)
        self.assertEqual(writes[0][:2], (f"/repos/{REPO}/issues/comments/3", "PATCH"))
        self.assertIn(MERGE, writes[0][2]["body"])
        self.assertIn(HEAD, writes[0][2]["body"])
        self.assertTrue(writes[0][2]["body"].startswith(report.MARKER))

    def test_creates_one_comment_when_no_owned_report_exists(self):
        api = FakeAPI()
        result = report.publish(self.root, self.summary, api, event(), self.env)
        self.assertTrue(result["published"])
        self.assertEqual(api.requests[-1][:2], (f"/repos/{REPO}/issues/42/comments", "POST"))

    def test_skips_outdated_head_and_base(self):
        for api in (FakeAPI(live_head="e" * 40), FakeAPI(live_base="f" * 40)):
            result = report.publish(self.root, self.summary, api, event(), self.env)
            self.assertFalse(result["published"])
            self.assertEqual(len(api.requests), 1)

    def test_fork_and_push_never_write(self):
        fork = event()
        fork["pull_request"]["head"]["repo"]["full_name"] = "attacker/fork"
        for payload, env in ((fork, self.env), (event(), {"GITHUB_EVENT_NAME": "push"})):
            api = FakeAPI()
            result = report.publish(self.root, self.summary, api, payload, env)
            self.assertFalse(result["published"])
            self.assertEqual(api.requests, [])

    def test_event_and_report_provenance_must_match(self):
        payload = event()
        payload["pull_request"]["head"]["sha"] = "e" * 40
        api = FakeAPI()
        with self.assertRaises(report.ReportError):
            report.publish(self.root, self.summary, api, payload, self.env)
        self.assertEqual(api.requests, [])

    def test_limits_comment_size_and_retains_provenance(self):
        self.summary.write_text("x" * 90000)
        api = FakeAPI()
        report.publish(self.root, self.summary, api, event(), self.env)
        text = api.requests[-1][2]["body"]
        self.assertLess(len(text), 60000)
        self.assertIn("truncated", text)
        self.assertIn(MERGE, text)


class PrepareTests(unittest.TestCase):
    def test_prepare_records_merge_and_pr_heads_separately_without_execution(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "scripts/coverage").mkdir(parents=True)
            (root / "scripts/coverage/crap_report.py").write_text(
                f'CRAPPER_REVISION = "{SCORER}"\nraise RuntimeError("must not import")\n')
            (root / ".metrics").mkdir()
            (root / ".metrics/inventory.csv").write_bytes(INVENTORY)
            env = {"GITHUB_SHA": MERGE, "GITHUB_EVENT_NAME": "pull_request", "GITHUB_REPOSITORY": REPO}
            with mock.patch("subprocess.check_output", return_value=MERGE + "\n"):
                data = report.prepare(root, event(), env)
            self.assertEqual(data["tested_sha"], MERGE)
            self.assertEqual(data["head_sha"], HEAD)
            self.assertEqual(data["base_sha"], BASE)
            self.assertEqual(data["inventory_sha256"], hashlib.sha256(INVENTORY).hexdigest())

    def test_push_identity_is_exact_tested_commit(self):
        data = report.event_identity({}, "push", BASE)
        self.assertEqual(data["head_sha"], BASE)
        self.assertEqual(data["tested_sha"], BASE)
        self.assertIsNone(data["base_sha"])

    def test_invalid_sha_cannot_be_used_as_git_or_api_input(self):
        with self.assertRaises(report.ReportError):
            report.event_identity({}, "push", "--malicious-option")


class BaselineSelectionContracts(unittest.TestCase):
    def api(self, runs=None, artifacts=None, data=None):
        api = FakeAPI()
        original = api.pages
        def pages(path, key=None):
            if key == "workflow_runs" and runs is not None:
                return iter(runs)
            if key == "artifacts" and artifacts is not None:
                return iter(artifacts)
            return original(path, key)
        api.pages = pages
        if data is not None:
            api.download_artifact = mock.Mock(return_value=data)
        return api

    def test_repository_and_base_are_checked_before_remote_lookup(self):
        for current in (metadata(repository="other/repo", base_sha=BASE), metadata(base_sha="bad")):
            api = self.api()
            api.pages = mock.Mock()
            with self.subTest(current=current), self.assertRaises(report.ReportError):
                report.find_baseline(api, current)
            api.pages.assert_not_called()

    def test_no_retained_candidate_is_not_a_zero_baseline(self):
        valid = list(FakeAPI().pages("", "workflow_runs"))[0]
        for api in (self.api(runs=[]), self.api(runs=[dict(valid, id=0)]),
                    self.api(runs=[dict(valid, id=True)]), self.api(artifacts=[]),
                    self.api(artifacts=[{"name": "crap-report", "id": 1, "expired": True}]),
                    self.api(artifacts=[{"name": "crap-report", "id": 0}]),
                    self.api(artifacts=[{"name": "crap-report", "id": True}])):
            with self.subTest(api=api):
                inventory, status = report.find_baseline(api, metadata(base_sha=BASE))
                self.assertIsNone(inventory)
                self.assertFalse(status["available"])
                self.assertIn("no retained", status["reason"])

    def test_corrupt_latest_artifact_falls_back_to_verified_older_candidate(self):
        data = archive(metadata(run_id="8"))
        artifacts = [{"name": "crap-report", "id": 4, "digest": "sha256:wrong"},
                     {"name": "crap-report", "id": 3,
                      "digest": "sha256:" + hashlib.sha256(data).hexdigest()}]
        api = self.api(artifacts=artifacts, data=data)
        inventory, status = report.find_baseline(api, metadata(base_sha=BASE))
        self.assertEqual(inventory, INVENTORY)
        self.assertEqual(status["artifact_id"], 3)
        self.assertEqual(api.download_artifact.call_args_list, [mock.call(4), mock.call(3)])
        api = self.api(artifacts=artifacts[:1], data=data)
        inventory, status = report.find_baseline(api, metadata(base_sha=BASE))
        self.assertIsNone(inventory)
        self.assertIn("digest differs", status["reason"])

    def test_baseline_writes_only_verified_inventory_and_push_clears_stale(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            meta = root / ".metrics/report-meta.json"
            report.write_json(meta, metadata(base_sha=BASE, event_name="pull_request"))
            with mock.patch("builtins.print"):
                unavailable = report.baseline(root, self.api(runs=[]))
            self.assertFalse(unavailable["available"])
            self.assertFalse((root / ".metrics/base-inventory.csv").exists())
            result = report.baseline(root, FakeAPI())
            self.assertTrue(result["available"])
            self.assertEqual((root / ".metrics/base-inventory.csv").read_bytes(), INVENTORY)
            report.write_json(meta, metadata())
            with mock.patch("builtins.print"):
                result = report.baseline(root, None)
            self.assertFalse(result["available"])
            self.assertFalse((root / ".metrics/base-inventory.csv").exists())
            report.write_json(meta, metadata(base_sha=BASE, event_name="pull_request"))
            with mock.patch("builtins.print"):
                result = report.baseline(root, None)
            self.assertIn("token is unavailable", result["reason"])


class RemoteTransportContracts(unittest.TestCase):
    def test_api_request_encodes_json_and_reads_bounded_response(self):
        api = report.GitHub(REPO, "secret")
        response = mock.MagicMock()
        response.__enter__.return_value = io.BytesIO(b'{"id": 9}')
        api.opener.open = mock.Mock(return_value=response)
        self.assertEqual(api.request("/comments", "POST", {"body": "hello"}), {"id": 9})
        request = api.opener.open.call_args.args[0]
        self.assertEqual(request.get_method(), "POST")
        self.assertEqual(json.loads(request.data), {"body": "hello"})
        for path in ("relative", "//foreign"):
            with self.subTest(path=path), self.assertRaises(report.ReportError):
                api.request(path)

    def test_download_direct_response_and_http_failures(self):
        api = report.GitHub(REPO, "secret")
        response = mock.MagicMock()
        response.read.return_value = b"archive"
        api.opener.open = mock.Mock(return_value=response)
        self.assertEqual(api.download_artifact(1), b"archive")
        for redirects in (False, True):
            errors = [urllib.error.HTTPError("https://api.github.com/a", 403, "denied", {}, None)]
            if redirects:
                errors.insert(0, urllib.error.HTTPError("https://api.github.com/a", 302, "redirect",
                                                       {"Location": "https://blob.example/a"}, None))
            api.opener.open = mock.Mock(side_effect=errors)
            with self.subTest(redirects=redirects), self.assertRaises(urllib.error.HTTPError):
                api.download_artifact(1)

    def test_relative_redirects_are_unsigned_and_bounded(self):
        api = report.GitHub(REPO, "secret")
        api.opener.open = mock.Mock(side_effect=[
            urllib.error.HTTPError("https://api.github.com/a", 302, "redirect",
                                   {"Location": "https://blob.example/a" if i == 0 else "/next"}, None)
            for i in range(6)])
        with self.assertRaisesRegex(report.ReportError, "redirect limit"):
            api.download_artifact(1)
        self.assertEqual(api.opener.open.call_count, 6)
        for call in api.opener.open.call_args_list[1:]:
            self.assertIsNone(call.args[0].get_header("Authorization"))
        self.assertEqual(api.opener.open.call_args.args[0].full_url, "https://blob.example/next")

    def test_invalid_identity_urls_and_pagination_are_rejected(self):
        for repo, url in (("invalid", "https://api.github.com"),
                          (REPO, "https://api.github.com?query=1")):
            with self.subTest(repo=repo, url=url), self.assertRaises(report.ReportError):
                report.GitHub(repo, "token", url)
        api = report.GitHub(REPO, "token")
        api.request = mock.Mock(return_value={"jobs": "invalid"})
        with self.assertRaisesRegex(report.ReportError, "list response"):
            list(api.pages("/jobs?filter=latest", "jobs"))
        api.request = mock.Mock(return_value=[{}] * 100)
        with self.assertRaisesRegex(report.ReportError, "pagination limit"):
            list(api.pages("/comments"))
        self.assertEqual(api.request.call_count, 10)
        self.assertIsNone(report.NoRedirect().redirect_request(None, None, 302, "", {}, "https://x"))


class CommandContracts(unittest.TestCase):
    def test_real_prepare_and_baseline_cli_artifacts(self):
        import os
        import subprocess
        import sys
        script = Path(report.__file__).resolve()
        git_dir = subprocess.check_output(["git", "rev-parse", "--absolute-git-dir"],
                                          cwd=script.parent, text=True).strip()
        revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=script.parent,
                                           text=True).strip()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "scripts/coverage").mkdir(parents=True)
            (root / "scripts/coverage/crap_report.py").write_text(f'CRAPPER_REVISION = "{SCORER}"\n')
            (root / ".metrics").mkdir()
            (root / ".metrics/inventory.csv").write_bytes(INVENTORY)
            event_path = root / "event.json"
            event_path.write_text(json.dumps(event()))
            env = dict(os.environ, GIT_DIR=git_dir, GITHUB_SHA=revision,
                       GITHUB_EVENT_PATH=str(event_path), GITHUB_EVENT_NAME="pull_request",
                       GITHUB_REPOSITORY=REPO)
            env.pop("GH_TOKEN", None)
            for command in ("prepare", "baseline"):
                result = subprocess.run([sys.executable, str(script), command, "--root", str(root)],
                                        env=env, text=True, capture_output=True)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            meta = json.loads((root / ".metrics/report-meta.json").read_text())
            self.assertEqual(meta["tested_sha"], revision)
            self.assertEqual(meta["head_sha"], HEAD)
            self.assertFalse(json.loads((root / ".metrics/baseline-status.json").read_text())["available"])
            for command, extra_env in (("publish", {}), ("prepare", {"GITHUB_SHA": BASE})):
                result = subprocess.run([sys.executable, str(script), command, "--root", str(root)],
                                        env=dict(env, **extra_env), text=True, capture_output=True)
                self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
                self.assertIn("error:", result.stdout)
            event_path.write_text("[]")
            with mock.patch.dict("os.environ", env, clear=True), self.assertRaisesRegex(report.ReportError, "event payload"):
                report.load_event()
            with mock.patch.dict("os.environ", {}, clear=True), self.assertRaises(report.ReportError):
                report.load_event()

    def test_main_publish_uses_real_provenance_and_persists_status(self):
        import os
        import runpy
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            report.write_json(root / ".metrics/report-meta.json", metadata(
                event_name="pull_request", base_sha=BASE, head_sha=HEAD, tested_sha=MERGE, pr_number=42))
            summary = root / "summary.md"
            summary.write_text("Measured report")
            payload = root / "event.json"
            payload.write_text(json.dumps(event()))
            env = {"GH_TOKEN": "test-token", "GITHUB_REPOSITORY": REPO,
                   "GITHUB_EVENT_NAME": "pull_request", "GITHUB_EVENT_PATH": str(payload)}
            api = FakeAPI()
            # Keep main/event/provenance/publishing real; isolate only GitHub transport.
            with mock.patch.dict(os.environ, env, clear=True), mock.patch.object(report.GitHub, "request", api.request), mock.patch.object(report.GitHub, "pages", api.pages):
                self.assertEqual(report.main(["publish", "--root", str(root), "--summary", str(summary)]), 0)
            self.assertTrue(json.loads((root / ".metrics/publish-status.json").read_text())["published"])
            (root / "scripts/coverage").mkdir(parents=True)
            (root / "scripts/coverage/crap_report.py").write_text(f'CRAPPER_REVISION = "{SCORER}"\n')
            (root / ".metrics/inventory.csv").write_bytes(INVENTORY)
            with mock.patch.dict(os.environ, env, clear=True), mock.patch.object(report.subprocess, "check_output", return_value=MERGE + "\n"):
                self.assertEqual(report.main(["prepare", "--root", str(root)]), 0)
            self.assertEqual(json.loads((root / ".metrics/report-meta.json").read_text())["tested_sha"], MERGE)
            with mock.patch.dict(os.environ, {}, clear=True):
                self.assertEqual(report.main(["baseline", "--root", str(root)]), 0)
            self.assertFalse(json.loads((root / ".metrics/baseline-status.json").read_text())["available"])
            with mock.patch.dict(os.environ, env, clear=True), mock.patch.object(report.subprocess, "check_output", return_value=MERGE):
                with self.assertRaisesRegex(report.ReportError, "checkout differs"):
                    report.prepare(root, event(), {"GITHUB_SHA": BASE})
            with mock.patch.dict(os.environ, {}, clear=True), mock.patch("sys.argv", ["ci_report.py", "baseline", "--root", str(root)]):
                with self.assertRaises(SystemExit) as success:
                    runpy.run_module("ci_report", run_name="__main__")
                self.assertEqual(success.exception.code, 0)
            with mock.patch.dict(os.environ, {}, clear=True), mock.patch("sys.argv", ["ci_report.py", "publish"]):
                with self.assertRaises(SystemExit) as result:
                    runpy.run_module("ci_report", run_name="__main__")
                self.assertEqual(result.exception.code, 2)


class RemainingValidationContracts(unittest.TestCase):
    def test_invalid_event_inventory_and_missing_scorer_are_errors(self):
        for number in (0, True):
            with self.subTest(number=number), self.assertRaises(report.ReportError):
                report.event_identity(dict(event(), number=number), "pull_request", MERGE)
        for data in (b"", b"\xff", b"wrong,columns\n"):
            with self.subTest(data=data), self.assertRaises(report.ReportError):
                report.inventory_header(data)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "scripts/coverage").mkdir(parents=True)
            path = root / "scripts/coverage/crap_report.py"
            path.write_text('"docstring"\nOTHER = 1\n')
            with self.assertRaisesRegex(report.ReportError, "scorer revision is missing"):
                report.scorer_revision(root)
        self.assertEqual(report.safe_error(OSError("private details")), "report request failed (OSError)")

    def test_archive_bounds_invalid_zip_and_metadata_shape(self):
        with mock.patch.object(report, "MAX_ARCHIVE_BYTES", 1), self.assertRaisesRegex(report.ReportError, "size limit"):
            report.read_baseline_archive(b"xx", metadata(), BASE)
        with self.assertRaisesRegex(report.ReportError, "invalid report archive"):
            report.read_baseline_archive(b"not a zip", metadata(), BASE)
        output = io.BytesIO()
        with zipfile.ZipFile(output, "w") as handle:
            handle.writestr("inventory.csv", INVENTORY)
            handle.writestr("report-meta.json", "[]")
        with self.assertRaisesRegex(report.ReportError, "invalid baseline metadata"):
            report.read_baseline_archive(output.getvalue(), metadata(), BASE)
        data = archive(entries=[(f"extra/{i}", "") for i in range(1999)])
        with self.assertRaisesRegex(report.ReportError, "too many entries"):
            report.read_baseline_archive(data, metadata(), BASE)

    def test_publish_rejects_mismatched_number_repository_and_run(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            summary = root / "summary.md"
            summary.write_text("report")
            valid = metadata(event_name="pull_request", base_sha=BASE, head_sha=HEAD,
                             tested_sha=MERGE, pr_number=42)
            for change in ({"pr_number": 9}, {"repository": "other/repo"}, {"run_id": "not-number"}):
                report.write_json(root / ".metrics/report-meta.json", dict(valid, **change))
                api = FakeAPI()
                with self.subTest(change=change), self.assertRaises(report.ReportError):
                    report.publish(root, summary, api, event(), {"GITHUB_EVENT_NAME": "pull_request"})
                self.assertFalse(any(method != "GET" for _, method, _ in api.requests))


if __name__ == "__main__":
    unittest.main()
