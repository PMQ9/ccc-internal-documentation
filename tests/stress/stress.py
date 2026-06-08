#!/usr/bin/env python3
"""Concurrency / load driver for the CCC BookStack stack — standard library only.

Three modes (the cases bash can't express cleanly):

  read       T-018/PERF-002: N workers each issue R requests against read surfaces.
             PASS iff zero 5xx and zero connection errors. Reports p50/p95 + throughput.

  edit       T-019/PERF-003: K workers concurrently PUT the same page with unique
             bodies. PASS (here) iff zero 5xx and zero connection errors. The runner

  mixed      T-024: K workers mix reads (book list, page get) and writes (page edit)
             in a single workload. PASS iff zero 5xx and zero connection errors. This
             catches the kind of regression BookStack 26.05 would have with read-write
             contention that pure-read or pure-edit tests miss. Uses the same page for
             edits (like edit mode) and rotates through read paths (like read mode).
         additionally asserts the DB revision-chain invariant afterwards.

Exit code is 0 only when no 5xx and no transport errors occurred — so it doubles
as a CI gate even without parsing the JSON summary it prints to stdout.
"""
from __future__ import annotations

import argparse
import json
import sys
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor, as_completed


def _request(url: str, token: str, method: str = "GET", body: bytes | None = None,
             timeout: float = 30.0) -> tuple[int, float, str]:
    """Return (status_code, elapsed_seconds, error_str). status 0 => transport error."""
    headers = {"Authorization": f"Token {token}"}
    if body is not None:
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=body, headers=headers, method=method)
    start = time.monotonic()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            resp.read()
            return resp.status, time.monotonic() - start, ""
    except urllib.error.HTTPError as e:
        # An HTTP error is still a *response* (e.g. 404/422) — record its code.
        return e.code, time.monotonic() - start, ""
    except (urllib.error.URLError, TimeoutError, OSError) as e:
        return 0, time.monotonic() - start, repr(e)


def _percentile(values: list[float], pct: float) -> float:
    if not values:
        return 0.0
    s = sorted(values)
    k = max(0, min(len(s) - 1, int(round((pct / 100.0) * (len(s) - 1)))))
    return s[k]


def run_reads(base: str, token: str, paths: list[str], concurrency: int, per_worker: int) -> dict:
    targets = [f"{base}{paths[i % len(paths)]}" for i in range(concurrency * per_worker)]

    def work(url: str) -> tuple[int, float, str]:
        return _request(url, token, "GET")

    return _drive(work, targets, concurrency)


def run_edits(base: str, token: str, page_id: int, concurrency: int, per_worker: int) -> dict:
    url = f"{base}/api/pages/{page_id}"
    jobs = [(i, j) for i in range(concurrency) for j in range(per_worker)]

    def work(job: tuple[int, int]) -> tuple[int, float, str]:
        w, n = job
        body = json.dumps(
            {"markdown": f"# concurrent edit w{w}-n{n}-{time.monotonic_ns()}\nstress write"}
        ).encode()
        return _request(url, token, "PUT", body)

    return _drive(work, jobs, concurrency)


def run_mixed(base: str, token: str, page_id: int, concurrency: int, per_worker: int) -> dict:
    """Alternate between reads and edits on the same page.

    Each worker issues per_worker requests: even-numbered jobs are GETs (read),
    odd-numbered are PUTs (edit). This surfaces read-write contention bugs that
    pure-read or pure-edit tests miss.
    """
    read_urls = [
        f"{base}/icon.png",
        f"{base}/api/books",
        f"{base}/api/pages/{page_id}",
        f"{base}/login",
    ]
    edit_url = f"{base}/api/pages/{page_id}"
    jobs = [(i, j) for i in range(concurrency) for j in range(per_worker)]

    def work(job: tuple[int, int]) -> tuple[int, float, str]:
        w, n = job
        if n % 2 == 0:
            # Read: rotate through read paths
            path = read_urls[(w + n) % len(read_urls)]
            return _request(path, token, "GET")
        else:
            # Write: edit the page with a unique body
            body = json.dumps(
                {"markdown": f"# mixed w{w}-n{n}-{time.monotonic_ns()}\nmixed write"}
            ).encode()
            return _request(edit_url, token, "PUT", body)

    return _drive(work, jobs, concurrency)


def _gate(summary: dict, max_p95_ms: float | None = None) -> tuple[bool, str]:
    """Pure pass/fail decision over a run summary — separated from main() so it can
    be unit-tested (stress_selftest.py) without driving real load.

    Always fails on any 5xx or transport error (the resilience contract). When
    --max-p95-ms is given, additionally enforces a p95 latency budget (the PERF SLO
    knob the test plan's non-functional checks call for).
    """
    if summary["server_errors_5xx"] > 0 or summary["transport_errors"] > 0:
        return False, (f"{summary['server_errors_5xx']} 5xx, "
                       f"{summary['transport_errors']} transport errors")
    if max_p95_ms is not None and summary["p95_ms"] > max_p95_ms:
        return False, f"p95 {summary['p95_ms']}ms exceeds budget {max_p95_ms}ms"
    return True, "ok"


def _drive(work, items, concurrency: int) -> dict:
    codes: dict[int, int] = {}
    latencies: list[float] = []
    errors: list[str] = []
    wall_start = time.monotonic()
    with ThreadPoolExecutor(max_workers=concurrency) as pool:
        futs = [pool.submit(work, it) for it in items]
        for fut in as_completed(futs):
            code, elapsed, err = fut.result()
            codes[code] = codes.get(code, 0) + 1
            latencies.append(elapsed)
            if err:
                errors.append(err)
    wall = time.monotonic() - wall_start

    total = len(items)
    server_errors = sum(c for code, c in codes.items() if code >= 500)
    transport_errors = codes.get(0, 0)
    successes = sum(c for code, c in codes.items() if 200 <= code < 400)
    return {
        "total": total,
        "concurrency": concurrency,
        "wall_seconds": round(wall, 3),
        "throughput_rps": round(total / wall, 1) if wall > 0 else None,
        "status_codes": {str(k): v for k, v in sorted(codes.items())},
        "successful": successes,
        "server_errors_5xx": server_errors,
        "transport_errors": transport_errors,
        "p50_ms": round(_percentile(latencies, 50) * 1000, 1),
        "p95_ms": round(_percentile(latencies, 95) * 1000, 1),
        "errors_sample": errors[:5],
    }


def main() -> int:
    ap = argparse.ArgumentParser(description="BookStack concurrency/load driver (stdlib only)")
    ap.add_argument("--base-url", required=True)
    ap.add_argument("--token", required=True, help="BookStack API token as 'id:secret'")
    ap.add_argument("--mode", choices=["read", "edit", "mixed"], required=True)
    ap.add_argument("--concurrency", type=int, default=20)
    ap.add_argument("--per-worker", type=int, default=10)
    ap.add_argument("--page-id", type=int, help="(edit mode) page to hammer")
    ap.add_argument("--paths", default="/icon.png,/api/books,/login",
                    help="(read mode) comma-separated paths to rotate through")
    ap.add_argument("--max-p95-ms", type=float, default=None,
                    help="optional PERF gate: fail (exit 1) if p95 latency exceeds this many ms")
    args = ap.parse_args()

    if args.mode == "read":
        summary = run_reads(args.base_url, args.token, args.paths.split(","),
                            args.concurrency, args.per_worker)
    elif args.mode == "mixed":
        if not args.page_id:
            ap.error("--page-id is required in mixed mode")
        summary = run_mixed(args.base_url, args.token, args.page_id,
                            args.concurrency, args.per_worker)
    else:
        if not args.page_id:
            ap.error("--page-id is required in edit mode")
        summary = run_edits(args.base_url, args.token, args.page_id,
                           args.concurrency, args.per_worker)

    print(json.dumps(summary, indent=2))
    passed, reason = _gate(summary, args.max_p95_ms)
    if not passed:
        print(f"FAIL: {reason}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
