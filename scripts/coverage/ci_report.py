#!/usr/bin/env python3
"""Record report provenance, fetch an exact baseline and maintain a PR comment."""

from __future__ import annotations

import argparse
import ast
import csv
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import urllib.error
import urllib.parse
import urllib.request
import zipfile

MARKER = "<!-- chronoverse-crap-report -->"
SCHEMA_VERSION = 1
SCOPE_VERSION = 2
MAX_ARCHIVE_BYTES = 20 * 1024 * 1024
MAX_INVENTORY_BYTES = 8 * 1024 * 1024
MAX_JSON_BYTES = 4 * 1024 * 1024
SHA = re.compile(r"[0-9a-f]{40,64}\Z")
REPOSITORY = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\Z")
FIELDS = {
    "path", "start_line", "end_line", "language", "namespace", "name",
    "complexity", "coverage", "coverage_recorded", "crap",
}


class ReportError(Exception):
    """A report or response cannot be safely used."""


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def bounded_read(response, limit: int) -> bytes:
    data = response.read(limit + 1)
    if len(data) > limit:
        raise ReportError("response exceeds the report size limit")
    return data


def require_https(url: str) -> None:
    parsed = urllib.parse.urlsplit(url)
    if parsed.scheme != "https" or not parsed.netloc or parsed.username or parsed.password:
        raise ReportError("GitHub downloads require HTTPS without URL credentials")


class GitHub:
    def __init__(self, repository: str, token: str, api_url: str = "https://api.github.com"):
        if not REPOSITORY.fullmatch(repository):
            raise ReportError("invalid repository identity")
        require_https(api_url)
        parsed = urllib.parse.urlsplit(api_url)
        if parsed.query or parsed.fragment:
            raise ReportError("invalid API URL")
        self.repository = repository
        self.api_url = api_url.rstrip("/")
        self.token = token
        self.opener = urllib.request.build_opener(NoRedirect())

    def request(self, path: str, method: str = "GET", data=None):
        if not path.startswith("/") or path.startswith("//"):
            raise ReportError("invalid API path")
        body = None if data is None else json.dumps(data).encode()
        request = urllib.request.Request(
            self.api_url + path,
            data=body,
            method=method,
            headers={
                "Authorization": "Bearer " + self.token,
                "Accept": "application/vnd.github+json",
                "Content-Type": "application/json",
                "X-GitHub-Api-Version": "2022-11-28",
            },
        )
        with self.opener.open(request, timeout=45) as response:
            return json.loads(bounded_read(response, MAX_JSON_BYTES))

    def download_artifact(self, artifact_id: int) -> bytes:
        path = f"/repos/{self.repository}/actions/artifacts/{artifact_id}/zip"
        request = urllib.request.Request(
            self.api_url + path,
            headers={"Authorization": "Bearer " + self.token,
                     "Accept": "application/vnd.github+json"},
        )
        try:
            response = self.opener.open(request, timeout=45)
        except urllib.error.HTTPError as error:
            if error.code not in (301, 302, 303, 307, 308):
                raise
            location = error.headers.get("Location", "")
            require_https(location)
            # A fresh request prevents forwarding the API token to blob storage.
            for _ in range(5):
                require_https(location)
                unsigned = urllib.request.Request(location)
                try:
                    response = self.opener.open(unsigned, timeout=60)
                except urllib.error.HTTPError as redirect:
                    if redirect.code not in (301, 302, 303, 307, 308):
                        raise
                    location = urllib.parse.urljoin(location, redirect.headers.get("Location", ""))
                else:
                    with response:
                        return bounded_read(response, MAX_ARCHIVE_BYTES)
            raise ReportError("artifact download exceeded the redirect limit")
        else:
            with response:
                return bounded_read(response, MAX_ARCHIVE_BYTES)

    def pages(self, path: str, key: str | None = None):
        separator = "&" if "?" in path else "?"
        for page in range(1, 11):
            payload = self.request(f"{path}{separator}per_page=100&page={page}")
            items = payload.get(key) if key and isinstance(payload, dict) else payload
            if not isinstance(items, list):
                raise ReportError("invalid GitHub list response")
            yield from items
            if len(items) < 100:
                return
        raise ReportError("GitHub pagination limit reached")


def write_json(path: Path, data) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")


def load_event() -> dict:
    path = os.environ.get("GITHUB_EVENT_PATH")
    if not path:
        raise ReportError("GITHUB_EVENT_PATH is required")
    event = json.loads(Path(path).read_text(encoding="utf-8"))
    if not isinstance(event, dict):
        raise ReportError("invalid event payload")
    return event


def checked_sha(value, name: str) -> str:
    if not isinstance(value, str) or not SHA.fullmatch(value):
        raise ReportError(f"invalid {name} revision")
    return value


def event_identity(event: dict, event_name: str, tested_sha: str) -> dict:
    tested_sha = checked_sha(tested_sha, "tested")
    if event_name == "pull_request":
        pr = event.get("pull_request", {})
        number = event.get("number")
        if type(number) is not int or number <= 0:
            raise ReportError("invalid pull request number")
        return {
            "base_sha": checked_sha(pr.get("base", {}).get("sha"), "base"),
            "head_sha": checked_sha(pr.get("head", {}).get("sha"), "head"),
            "tested_sha": tested_sha,
            "pr_number": number,
        }
    return {"base_sha": None, "head_sha": tested_sha, "tested_sha": tested_sha,
            "pr_number": None}


def scorer_revision(root: Path) -> str:
    tree = ast.parse((root / "scripts/coverage/crap_report.py").read_text(encoding="utf-8"))
    for node in tree.body:
        if isinstance(node, ast.Assign) and any(
            isinstance(target, ast.Name) and target.id == "CRAPPER_REVISION"
            for target in node.targets
        ):
            return checked_sha(ast.literal_eval(node.value), "scorer")
    raise ReportError("canonical scorer revision is missing")


def inventory_header(data: bytes) -> list[str]:
    try:
        fields = next(csv.reader(io.StringIO(data.decode("utf-8"))))
    except (UnicodeDecodeError, StopIteration, csv.Error) as error:
        raise ReportError("invalid inventory header") from error
    if set(fields) != FIELDS or len(fields) != len(FIELDS):
        raise ReportError("unsupported inventory columns")
    return fields


def prepare(root: Path, event: dict, env: dict = os.environ) -> dict:
    tested = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
    expected = env.get("GITHUB_SHA")
    if expected and expected != tested:
        raise ReportError("checkout differs from the workflow's tested revision")
    identity = event_identity(event, env.get("GITHUB_EVENT_NAME", ""), tested)
    inventory = (root / ".metrics/inventory.csv").read_bytes()
    inventory_header(inventory)
    metadata = {
        "schema_version": SCHEMA_VERSION,
        "scope_version": SCOPE_VERSION,
        "scorer_revision": scorer_revision(root),
        "reporter_sha256": hashlib.sha256((root / "scripts/coverage/crap_report.py").read_bytes()).hexdigest(),
        "inventory_sha256": hashlib.sha256(inventory).hexdigest(),
        "event_name": env.get("GITHUB_EVENT_NAME", ""),
        "repository": env.get("GITHUB_REPOSITORY", ""),
        "run_id": env.get("GITHUB_RUN_ID", ""),
        **identity,
    }
    write_json(root / ".metrics/report-meta.json", metadata)
    return metadata


def read_baseline_archive(data: bytes, expected: dict, base_sha: str, run_id: int | None = None) -> bytes:
    if len(data) > MAX_ARCHIVE_BYTES:
        raise ReportError("artifact exceeds the report size limit")
    try:
        archive = zipfile.ZipFile(io.BytesIO(data))
        with archive:
            members = archive.infolist()
            if len(members) > 2000:
                raise ReportError("artifact has too many entries")
            candidates = {"inventory": [], "metadata": []}
            for item in members:
                path = PurePosixPath(item.filename)
                if path.is_absolute() or ".." in path.parts or "\\" in item.filename:
                    raise ReportError("unsafe artifact entry path")
                if item.filename in (".metrics/inventory.csv", "inventory.csv"):
                    candidates["inventory"].append(item)
                if item.filename in (".metrics/report-meta.json", "report-meta.json"):
                    candidates["metadata"].append(item)
            if not candidates["metadata"]:
                raise ReportError("baseline artifact has no provenance metadata (first rollout)")
            if any(len(items) != 1 for items in candidates.values()):
                raise ReportError("artifact report entries are missing or ambiguous")
            inventory_info = candidates["inventory"][0]
            metadata_info = candidates["metadata"][0]
            if inventory_info.file_size > MAX_INVENTORY_BYTES or metadata_info.file_size > 65536:
                raise ReportError("artifact report entry is too large")
            with archive.open(inventory_info) as handle:
                inventory = bounded_read(handle, MAX_INVENTORY_BYTES)
            with archive.open(metadata_info) as handle:
                metadata = json.loads(bounded_read(handle, 65536))
    except (zipfile.BadZipFile, KeyError, ValueError, RuntimeError) as error:
        raise ReportError("invalid report archive") from error
    if not isinstance(metadata, dict):
        raise ReportError("invalid baseline metadata")
    for key in ("schema_version", "scope_version", "scorer_revision", "reporter_sha256", "repository"):
        if metadata.get(key) != expected.get(key):
            raise ReportError(f"baseline {key} does not match the current report")
    if metadata.get("event_name") != "push":
        raise ReportError("baseline is not a main push report")
    if metadata.get("head_sha") != base_sha or metadata.get("tested_sha") != base_sha:
        raise ReportError("baseline revision does not match the PR base")
    if metadata.get("inventory_sha256") != hashlib.sha256(inventory).hexdigest():
        raise ReportError("baseline inventory does not match its provenance hash")
    if run_id is not None and metadata.get("run_id") != str(run_id):
        raise ReportError("baseline run identity differs from the artifact owner")
    inventory_header(inventory)
    return inventory


def successful_report_job(api: GitHub, run_id: int) -> bool:
    return any(job.get("name") == "CRAP report" and job.get("status") == "completed"
               and job.get("conclusion") == "success"
               for job in api.pages(f"/repos/{api.repository}/actions/runs/{run_id}/jobs?filter=latest", "jobs"))


def find_baseline(api: GitHub, current: dict) -> tuple[bytes | None, dict]:
    if current.get("repository") != api.repository:
        raise ReportError("current report repository differs from the workflow repository")
    base_sha = checked_sha(current.get("base_sha"), "base")
    query = urllib.parse.urlencode({"head_sha": base_sha, "event": "push", "branch": "main"})
    runs = api.pages(f"/repos/{api.repository}/actions/workflows/ci.yaml/runs?{query}", "workflow_runs")
    reasons = []
    for run in runs:
        if (run.get("head_sha") != base_sha or run.get("event") != "push"
                or run.get("head_branch") != "main"
                or run.get("head_repository", {}).get("full_name") != api.repository):
            continue
        run_id = run.get("id")
        if type(run_id) is not int or run_id <= 0:
            continue
        artifacts = api.pages(f"/repos/{api.repository}/actions/runs/{run_id}/artifacts", "artifacts")
        reports = [item for item in artifacts if item.get("name") == "crap-report" and not item.get("expired")]
        if not reports:
            continue
        if not successful_report_job(api, run_id):
            reasons.append("the exact-base CRAP report job has not succeeded")
            continue
        for artifact in sorted(reports, key=lambda item: item.get("id", 0), reverse=True):
            artifact_id = artifact.get("id")
            if type(artifact_id) is not int or artifact_id <= 0:
                continue
            try:
                data = api.download_artifact(artifact_id)
                digest = artifact.get("digest")
                if digest and digest != "sha256:" + hashlib.sha256(data).hexdigest():
                    raise ReportError("downloaded artifact digest differs from GitHub metadata")
                inventory = read_baseline_archive(data, current, base_sha, run_id)
                return inventory, {"available": True, "reason": "exact-base report verified",
                                   "base_sha": base_sha, "run_id": run_id,
                                   "run_url": run.get("html_url", ""), "artifact_id": artifact_id}
            except ReportError as error:
                reasons.append(str(error))
    reason = reasons[-1] if reasons else "no retained, successful CRAP report artifact exists for the exact PR base"
    return None, {"available": False, "base_sha": base_sha, "reason": reason}


def safe_error(error: Exception) -> str:
    if isinstance(error, urllib.error.HTTPError):
        return f"GitHub request returned HTTP {error.code}"
    if isinstance(error, ReportError):
        return str(error)
    return f"report request failed ({type(error).__name__})"


def baseline(root: Path, api: GitHub | None) -> dict:
    destination = root / ".metrics/base-inventory.csv"
    destination.unlink(missing_ok=True)
    current = json.loads((root / ".metrics/report-meta.json").read_text(encoding="utf-8"))
    if current.get("event_name") != "pull_request":
        status = {"available": False, "reason": "base comparison applies to pull requests only"}
    else:
        try:
            if api is None:
                raise ReportError("GitHub token is unavailable for the exact-base artifact lookup")
            inventory, status = find_baseline(api, current)
            if inventory is not None:
                destination.write_bytes(inventory)
        except (ReportError, urllib.error.URLError, ValueError, OSError) as error:
            status = {"available": False, "base_sha": current.get("base_sha"), "reason": safe_error(error)}
    write_json(root / ".metrics/baseline-status.json", status)
    if not status["available"]:
        message = status["reason"].replace("\r", " ").replace("\n", " ").replace("%", "%25")
        print(f"::warning title=CRAP comparison unavailable::{message}")
    return status


def publish(root: Path, summary: Path, api: GitHub, event: dict, env: dict = os.environ) -> dict:
    metadata = json.loads((root / ".metrics/report-meta.json").read_text(encoding="utf-8"))
    if env.get("GITHUB_EVENT_NAME") != "pull_request":
        return {"published": False, "reason": "not a pull request event"}
    payload = event.get("pull_request", {})
    if payload.get("head", {}).get("repo", {}).get("full_name") != api.repository:
        return {"published": False, "reason": "fork comments require a trusted publishing workflow; summary and artifact remain available"}
    number = event.get("number")
    if type(number) is not int or number <= 0 or metadata.get("pr_number") != number:
        raise ReportError("PR metadata differs from the workflow event")
    if metadata.get("repository") != api.repository:
        raise ReportError("report repository differs from the publishing repository")
    expected_head = checked_sha(payload.get("head", {}).get("sha"), "head")
    expected_base = checked_sha(payload.get("base", {}).get("sha"), "base")
    if metadata.get("head_sha") != expected_head or metadata.get("base_sha") != expected_base:
        raise ReportError("report provenance differs from the workflow event")
    current = api.request(f"/repos/{api.repository}/pulls/{number}")
    if (current.get("state") != "open" or current.get("head", {}).get("sha") != expected_head
            or current.get("base", {}).get("sha") != expected_base):
        return {"published": False, "reason": "PR head/base changed or PR closed; stale report was not published"}
    text = summary.read_text(encoding="utf-8")
    tested = checked_sha(metadata.get("tested_sha"), "tested")
    run_id = str(metadata.get("run_id", ""))
    if not run_id.isdigit():
        raise ReportError("invalid workflow run identity")
    server = env.get("GITHUB_SERVER_URL", "https://github.com").rstrip("/")
    require_https(server)
    link = f"{server}/{api.repository}/actions/runs/{run_id}"
    footer = f"\n\n[Workflow run]({link}) · PR head `{expected_head}` · tested merge `{tested}` · base `{expected_base}`\n"
    prefix = MARKER + "\n\n"
    if len(prefix + text + footer) > 60000:
        text = text[: 59000 - len(prefix + footer)] + "\n\nReport truncated; download the full artifact from the workflow run.\n"
    body = prefix + text + footer
    matches = [item for item in api.pages(f"/repos/{api.repository}/issues/{number}/comments")
               if item.get("user", {}).get("login") == "github-actions[bot]"
               and item.get("user", {}).get("type") == "Bot"
               and isinstance(item.get("body"), str) and MARKER in item["body"]]
    if matches:
        comment = min(matches, key=lambda item: item["id"])
        response = api.request(f"/repos/{api.repository}/issues/comments/{comment['id']}", "PATCH", {"body": body})
    else:
        response = api.request(f"/repos/{api.repository}/issues/{number}/comments", "POST", {"body": body})
    return {"published": True, "comment_id": response["id"], "run_url": link}


def github_from_environment() -> GitHub | None:
    token = os.environ.get("GH_TOKEN")
    if not token:
        return None
    return GitHub(os.environ.get("GITHUB_REPOSITORY", ""), token,
                  os.environ.get("GITHUB_API_URL", "https://api.github.com"))


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("prepare", "baseline", "publish"))
    parser.add_argument("--root", type=Path, default=Path("."))
    parser.add_argument("--summary", type=Path, default=Path("target/coverage/pr-summary.md"))
    args = parser.parse_args(argv)
    root = args.root.resolve()
    if args.command == "prepare":
        result = prepare(root, load_event())
    elif args.command == "baseline":
        result = baseline(root, github_from_environment())
    else:
        api = github_from_environment()
        if api is None:
            raise ReportError("GitHub token is required to publish a PR comment")
        summary = args.summary if args.summary.is_absolute() else root / args.summary
        result = publish(root, summary, api, load_event())
        write_json(root / ".metrics/publish-status.json", result)
    print(json.dumps(result))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ReportError, urllib.error.URLError, ValueError, OSError) as error:
        print("error: " + safe_error(error))
        raise SystemExit(2)
