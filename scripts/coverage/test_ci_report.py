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


if __name__ == "__main__":
    unittest.main()
