#!/usr/bin/env python3
"""Inspect ColabOne GitHub Actions release runs, retry failed jobs, and verify GitHub Releases."""

from __future__ import annotations

import argparse
import http.client
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request


_SEMVER_CORE = r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)"
_SEMVER_PRERELEASE = (
    r"(?:-(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
    r"(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?"
)
COLABONE_RELEASE_TAG_RE = re.compile(rf"^v({_SEMVER_CORE}{_SEMVER_PRERELEASE})$")
COLABONE_WORKFLOW_CHOICES = ("release.yml", "ci.yml")


class ColabOneActionsError(Exception):
    def __init__(self, code: str, **details):
        super().__init__(code)
        self.details = {"code": code, **details}


def colabone_expected_release_assets(tag: str) -> list[str]:
    match = COLABONE_RELEASE_TAG_RE.fullmatch(tag)
    if match is None:
        raise ColabOneActionsError("invalid_release_tag", tag=tag)
    version = match.group(1)
    return [
        f"ColabOne-{version}-windows-x64.zip",
        f"ColabOne-{version}-macos-arm64.zip",
        "SHA256SUMS.txt",
    ]


def colabone_run_command(
    root: Path, args: list[str], *, input_text: str | None = None, env=None
) -> str:
    try:
        result = subprocess.run(
            args,
            cwd=root,
            input=input_text,
            stdin=None if input_text is not None else subprocess.DEVNULL,
            env=env,
            capture_output=True,
            text=True,
            encoding="utf-8",
            timeout=30,
            check=False,
        )
    except FileNotFoundError:
        return ""
    if result.returncode:
        return ""
    return result.stdout.strip()


def colabone_parse_repository(remote: str) -> tuple[str, str]:
    if remote.startswith("git@"):
        match = re.fullmatch(r"git@([^:]+):(.+)", remote)
        if not match:
            raise ColabOneActionsError("invalid_remote")
        host, path = match.groups()
    else:
        parsed = urllib.parse.urlsplit(remote)
        if parsed.scheme not in {"https", "ssh"}:
            raise ColabOneActionsError("unsupported_remote")
        host, path = parsed.hostname, parsed.path
    if host != "github.com":
        raise ColabOneActionsError("unsupported_github_host", host=host)
    path = path.strip("/").removesuffix(".git")
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", path):
        raise ColabOneActionsError("invalid_repository")
    return host, path


def colabone_resolve_credential(root: Path, remote: str, host: str) -> str:
    for name in ("GH_TOKEN", "GITHUB_TOKEN"):
        if os.environ.get(name):
            return os.environ[name]
    token = colabone_run_command(root, ["gh", "auth", "token", "--hostname", host])
    if token:
        return token
    if remote.startswith("https://"):
        env = {**os.environ, "GIT_TERMINAL_PROMPT": "0", "GCM_INTERACTIVE": "never"}
        result = colabone_run_command(
            root,
            ["git", "credential", "fill"],
            input_text=f"url={remote}\n\n",
            env=env,
        )
        fields = dict(
            line.split("=", 1) for line in result.splitlines() if "=" in line
        )
        if fields.get("password"):
            return fields["password"]
    raise ColabOneActionsError("credential_unavailable", host=host)


class _NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class ColabOneActionsClient:
    def __init__(self, repo: str, token: str):
        self.base = f"https://api.github.com/repos/{repo}/"
        self.token = token
        self.opener = urllib.request.build_opener(_NoRedirectHandler)

    def request(self, path: str, *, method: str = "GET"):
        request = urllib.request.Request(
            self.base + path,
            method=method,
            headers={
                "Authorization": f"Bearer {self.token}",
                "Accept": "application/vnd.github+json",
                "X-GitHub-Api-Version": "2022-11-28",
                "User-Agent": "ColabOne-release",
            },
        )
        try:
            try:
                response = self.opener.open(request, timeout=30)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                status, headers, raw = response.status, response.headers, response.read()
        except (
            urllib.error.URLError,
            TimeoutError,
            OSError,
            http.client.HTTPException,
        ) as error:
            raise ColabOneActionsError(
                "transport_error", method=method, endpoint=path, reason=str(error)
            ) from error
        if not 200 <= status < 300 and not (status == 302 and method == "GET"):
            raise ColabOneActionsError(
                "http_error",
                status=status,
                method=method,
                endpoint=path,
                response=raw.decode("utf-8", errors="replace"),
                headers=dict(headers),
            )
        return status, headers, raw

    def json(self, path: str) -> dict:
        status, _, raw = self.request(path)
        if status != 200:
            raise ColabOneActionsError("unexpected_status", status=status, endpoint=path)
        try:
            data = json.loads(raw)
        except (ValueError, UnicodeError) as error:
            raise ColabOneActionsError(
                "invalid_json_response",
                endpoint=path,
                response=raw.decode("utf-8", errors="replace"),
            ) from error
        if not isinstance(data, dict):
            raise ColabOneActionsError(
                "invalid_json_response", endpoint=path, response=data
            )
        return data

    def pages(self, path: str, key: str, **filters) -> list[dict]:
        items: list[dict] = []
        page = 1
        while True:
            query = urllib.parse.urlencode({**filters, "per_page": 100, "page": page})
            data = self.json(f"{path}?{query}")
            batch = data.get(key)
            if not isinstance(batch, list) or any(
                not isinstance(item, dict) for item in batch
            ):
                raise ColabOneActionsError(
                    "invalid_page_response", endpoint=path, response=data
                )
            items.extend(batch)
            if len(batch) < 100:
                return items
            page += 1

    def download(self, path: str, output: Path) -> dict:
        # Exclusive creation keeps pre-existing logs intact, including on failure.
        destination = output.open("xb")
        written = 0
        try:
            with destination:
                status, headers, raw = self.request(path)
                if status == 302:
                    url = headers.get("Location", "")
                    parsed = urllib.parse.urlsplit(url)
                    if parsed.scheme != "https" or not parsed.hostname or parsed.username:
                        raise ColabOneActionsError("invalid_log_redirect")
                    # Signed storage URLs must be fetched without GitHub credentials.
                    with urllib.request.urlopen(url, timeout=30) as response:
                        while chunk := response.read(256 * 1024):
                            written += destination.write(chunk)
                        expected = response.headers.get("Content-Length")
                        if expected is not None and written != int(expected):
                            raise ColabOneActionsError(
                                "incomplete_log_download",
                                expected=int(expected),
                                actual=written,
                            )
                elif status == 200:
                    written = destination.write(raw)
                else:
                    raise ColabOneActionsError("unexpected_status", status=status)
        except BaseException:
            output.unlink(missing_ok=True)
            raise
        return {"output": str(output.resolve()), "bytes": written}


def colabone_read_run(
    client: ColabOneActionsClient, run_id: int, attempt: int | None = None
) -> dict:
    path = f"actions/runs/{run_id}"
    if attempt is not None:
        path += f"/attempts/{attempt}"
    run = client.json(path)
    if (
        run.get("id") != run_id
        or type(run.get("run_attempt")) is not int
        or run["run_attempt"] < 1
        or (attempt is not None and run["run_attempt"] != attempt)
        or any(
            not isinstance(run.get(key), str)
            for key in ("path", "event", "head_sha", "status")
        )
        or not isinstance(run.get("head_branch"), (str, type(None)))
        or not isinstance(run.get("conclusion"), (str, type(None)))
    ):
        raise ColabOneActionsError("invalid_run_response", endpoint=path, response=run)
    return run


def colabone_run_matches(run: dict, workflow: str, tag: str, sha: str) -> bool:
    return (
        isinstance(run.get("path"), str)
        and run["path"].split("@")[0] == f".github/workflows/{workflow}"
        and run.get("event") == "push"
        and run.get("head_branch") == tag
        and run.get("head_sha") == sha
    )


def colabone_summarize_run(run: dict) -> dict:
    return {
        key: run.get(key)
        for key in (
            "id",
            "run_attempt",
            "path",
            "event",
            "head_branch",
            "head_sha",
            "status",
            "conclusion",
            "html_url",
            "created_at",
            "updated_at",
        )
    }


def colabone_show_run(
    client: ColabOneActionsClient, run_id: int, attempt: int | None = None
) -> dict:
    run = colabone_read_run(client, run_id, attempt)
    result = colabone_summarize_run(run)
    # Attempt-scoped jobs prevent mixing old successes with current failures.
    jobs = client.pages(
        f"actions/runs/{run_id}/attempts/{run['run_attempt']}/jobs", "jobs"
    )
    result["jobs"] = [
        {
            **{
                key: job.get(key)
                for key in (
                    "id",
                    "name",
                    "status",
                    "conclusion",
                    "html_url",
                    "run_attempt",
                )
            },
            "steps": job.get("steps", []),
            "active_steps": [
                step
                for step in job.get("steps", [])
                if isinstance(step, dict) and step.get("status") == "in_progress"
            ],
        }
        for job in jobs
    ]
    return result


def colabone_rerun_failed(
    client: ColabOneActionsClient,
    run_id: int,
    workflow: str,
    tag: str,
    sha: str,
    attempt: int,
) -> dict:
    path = f"actions/runs/{run_id}"
    before = colabone_read_run(client, run_id)
    if not colabone_run_matches(before, workflow, tag, sha):
        raise ColabOneActionsError(
            "run_identity_mismatch", run=colabone_summarize_run(before)
        )
    if before["run_attempt"] != attempt:
        raise ColabOneActionsError(
            "run_attempt_changed", run=colabone_summarize_run(before)
        )
    if before["status"] != "completed" or before["conclusion"] not in {
        "failure",
        "timed_out",
    }:
        raise ColabOneActionsError("run_not_failed", run=colabone_summarize_run(before))
    try:
        status, _, raw = client.request(f"{path}/rerun-failed-jobs", method="POST")
        if status != 201:
            raise ColabOneActionsError(
                "unexpected_status",
                status=status,
                response=raw.decode("utf-8", errors="replace"),
            )
    except ColabOneActionsError as error:
        uncertain = error.details["code"] in {"transport_error", "unexpected_status"}
        uncertain = uncertain or (
            error.details["code"] == "http_error"
            and (error.details["status"] == 408 or error.details["status"] >= 500)
        )
        if not uncertain:
            raise
        try:
            current = colabone_read_run(client, run_id)
        except ColabOneActionsError as query_error:
            raise ColabOneActionsError(
                "rerun_outcome_unknown",
                request_error=error.details,
                query_error=query_error.details,
            ) from error
        if current["run_attempt"] > attempt and colabone_run_matches(
            current, workflow, tag, sha
        ):
            return {
                "accepted": True,
                "observed_after_request_error": True,
                "run": colabone_summarize_run(current),
            }
        raise ColabOneActionsError(
            "rerun_outcome_unknown",
            request_error=error.details,
            run=colabone_summarize_run(current),
        ) from error
    return {"accepted": True, "previous_attempt": attempt, "run_id": run_id}


def colabone_inspect_release(client: ColabOneActionsClient, tag: str) -> dict:
    expected_assets = colabone_expected_release_assets(tag)
    endpoint = f"releases/tags/{urllib.parse.quote(tag, safe='')}"
    release = client.json(endpoint)
    assets_raw = release.get("assets")
    if (
        release.get("tag_name") != tag
        or not isinstance(assets_raw, list)
        or any(not isinstance(asset, dict) for asset in assets_raw)
    ):
        raise ColabOneActionsError(
            "invalid_release_response", endpoint=endpoint, response=release
        )

    assets = [
        {
            key: asset.get(key)
            for key in ("id", "name", "size", "state", "browser_download_url")
        }
        for asset in assets_raw
    ]
    by_name = {
        asset["name"]: asset for asset in assets if isinstance(asset.get("name"), str)
    }
    missing_assets = [name for name in expected_assets if name not in by_name]
    incomplete_assets = [
        name
        for name in expected_assets
        if name in by_name
        and (
            by_name[name].get("state") != "uploaded"
            or not isinstance(by_name[name].get("size"), int)
            or by_name[name]["size"] <= 0
        )
    ]
    return {
        **{
            key: release.get(key)
            for key in (
                "id",
                "tag_name",
                "target_commitish",
                "name",
                "draft",
                "prerelease",
                "html_url",
                "published_at",
            )
        },
        "assets": assets,
        "expected_assets": expected_assets,
        "missing_assets": missing_assets,
        "incomplete_assets": incomplete_assets,
        "assets_complete": (
            not release.get("draft")
            and not missing_assets
            and not incomplete_assets
        ),
    }


def _positive(value: str) -> int:
    result = int(value)
    if result < 1:
        raise argparse.ArgumentTypeError("must be positive")
    return result


def build_parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__)
    result.add_argument(
        "--repo-root", type=Path, default=Path(__file__).resolve().parents[4]
    )
    commands = result.add_subparsers(dest="operation", required=True)
    for operation in ("list", "show", "logs", "rerun-failed", "release"):
        sub = commands.add_parser(operation)
        if operation in {"list", "rerun-failed"}:
            sub.add_argument(
                "--workflow",
                default="release.yml",
                choices=COLABONE_WORKFLOW_CHOICES,
            )
            sub.add_argument("--tag", required=True)
            sub.add_argument("--sha", required=True)
        if operation in {"show", "rerun-failed"}:
            sub.add_argument("--run-id", required=True, type=_positive)
            sub.add_argument(
                "--attempt", type=_positive, required=operation == "rerun-failed"
            )
        if operation == "logs":
            group = sub.add_mutually_exclusive_group(required=True)
            group.add_argument("--run-id", type=_positive)
            group.add_argument("--job-id", type=_positive)
            sub.add_argument("--attempt", type=_positive)
            sub.add_argument("--output", type=Path, required=True)
        if operation == "release":
            sub.add_argument("--tag", required=True)
    return result


def main(argv: list[str] | None = None) -> int:
    arguments = build_parser()
    args = arguments.parse_args(argv)
    if args.operation == "logs" and bool(args.run_id) != bool(args.attempt):
        arguments.error("run logs require --attempt; job logs do not accept --attempt")
    if args.operation in {"list", "rerun-failed"}:
        if not COLABONE_RELEASE_TAG_RE.fullmatch(args.tag) or not re.fullmatch(
            r"[0-9a-f]{40}", args.sha
        ):
            arguments.error(
                "provide a vMAJOR.MINOR.PATCH[-prerelease] tag and full 40-char commit SHA"
            )
    if args.operation == "release" and not COLABONE_RELEASE_TAG_RE.fullmatch(args.tag):
        arguments.error("provide a vMAJOR.MINOR.PATCH[-prerelease] tag")

    try:
        remote = colabone_run_command(
            args.repo_root, ["git", "remote", "get-url", "origin"]
        )
        host, repo = colabone_parse_repository(remote)
        client = ColabOneActionsClient(
            repo, colabone_resolve_credential(args.repo_root, remote, host)
        )
        if args.operation == "list":
            runs = client.pages(
                f"actions/workflows/{args.workflow}/runs",
                "workflow_runs",
                head_sha=args.sha,
                branch=args.tag,
                event="push",
            )
            data = {
                "runs": [
                    colabone_summarize_run(run)
                    for run in runs
                    if colabone_run_matches(run, args.workflow, args.tag, args.sha)
                ]
            }
        elif args.operation == "show":
            data = colabone_show_run(client, args.run_id, args.attempt)
        elif args.operation == "logs":
            path = (
                f"actions/jobs/{args.job_id}/logs"
                if args.job_id
                else f"actions/runs/{args.run_id}/attempts/{args.attempt}/logs"
            )
            data = client.download(path, args.output)
        elif args.operation == "release":
            data = colabone_inspect_release(client, args.tag)
        else:
            data = colabone_rerun_failed(
                client, args.run_id, args.workflow, args.tag, args.sha, args.attempt
            )
        # Standard ASCII JSON escapes preserve Unicode on non-UTF-8 output pipes.
        print(json.dumps(data))
        return 0
    except ColabOneActionsError as error:
        print(json.dumps({"error": error.details}), file=sys.stderr)
    except (
        OSError,
        ValueError,
        KeyError,
        http.client.HTTPException,
        subprocess.TimeoutExpired,
    ) as error:
        print(
            json.dumps(
                {"error": {"code": "local_or_protocol_error", "reason": str(error)}}
            ),
            file=sys.stderr,
        )
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
