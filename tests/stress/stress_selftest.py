#!/usr/bin/env python3
"""Offline unit tests for the stress driver's pure logic — stdlib `unittest`, no
network, no BookStack, no deps. The load runs (read/edit/mixed) need a live stack
(exercised by the integration runner), but the aggregation, percentile, and gate
math are pure functions and deserve fast, deterministic coverage of their own.

Run:  python3 tests/stress/stress_selftest.py        (or:  -m unittest)
"""
import os
import sys
import unittest

# Make `import stress` work no matter the CWD (repo root, tests/stress, or -m).
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import stress  # noqa: E402


class PercentileTests(unittest.TestCase):
    def test_empty_is_zero(self):
        self.assertEqual(stress._percentile([], 95), 0.0)

    def test_single_value(self):
        self.assertEqual(stress._percentile([0.5], 95), 0.5)

    def test_known_distribution(self):
        vals = [10.0, 20.0, 30.0]
        self.assertEqual(stress._percentile(vals, 0), 10.0)    # min
        self.assertEqual(stress._percentile(vals, 50), 20.0)   # median
        self.assertEqual(stress._percentile(vals, 100), 30.0)  # max

    def test_unsorted_input_is_sorted(self):
        # Percentile must not assume the caller pre-sorted.
        self.assertEqual(stress._percentile([30.0, 10.0, 20.0], 100), 30.0)


class GateTests(unittest.TestCase):
    def _summary(self, **over):
        base = {"server_errors_5xx": 0, "transport_errors": 0, "p95_ms": 100.0}
        base.update(over)
        return base

    def test_clean_run_passes(self):
        self.assertEqual(stress._gate(self._summary()), (True, "ok"))

    def test_any_5xx_fails(self):
        ok, reason = stress._gate(self._summary(server_errors_5xx=1))
        self.assertFalse(ok)
        self.assertIn("5xx", reason)

    def test_any_transport_error_fails(self):
        ok, reason = stress._gate(self._summary(transport_errors=3))
        self.assertFalse(ok)
        self.assertIn("transport", reason)

    def test_p95_budget_enforced_only_when_set(self):
        slow = self._summary(p95_ms=500.0)
        self.assertFalse(stress._gate(slow, max_p95_ms=300)[0])  # over budget
        self.assertTrue(stress._gate(slow, max_p95_ms=600)[0])   # under budget
        self.assertTrue(stress._gate(slow)[0])                   # no budget => ignored

    def test_5xx_beats_p95(self):
        # A 5xx fails regardless of a satisfied latency budget.
        ok, reason = stress._gate(self._summary(server_errors_5xx=1, p95_ms=1.0), max_p95_ms=10_000)
        self.assertFalse(ok)
        self.assertIn("5xx", reason)


class DriveAggregationTests(unittest.TestCase):
    def test_aggregates_codes_errors_and_successes(self):
        # Each item is a pre-canned (status, elapsed_s, error) result; the fake
        # work fn just echoes it, so _drive's accounting is what's under test.
        cases = [
            (200, 0.01, ""),
            (200, 0.02, ""),
            (404, 0.03, ""),
            (500, 0.04, ""),
            (0, 0.05, "boom"),  # transport error
        ]
        summary = stress._drive(lambda item: item, cases, concurrency=4)
        self.assertEqual(summary["total"], 5)
        self.assertEqual(summary["server_errors_5xx"], 1)
        self.assertEqual(summary["transport_errors"], 1)
        self.assertEqual(summary["successful"], 2)  # only the two 2xx
        self.assertEqual(summary["status_codes"], {"0": 1, "200": 2, "404": 1, "500": 1})
        self.assertEqual(summary["errors_sample"], ["boom"])

    def test_gate_consumes_drive_output(self):
        # End-to-end of the pure pipeline: a 5xx in the driven workload must make
        # the gate fail — the property the CI exit code depends on.
        cases = [(200, 0.01, ""), (503, 0.02, "")]
        summary = stress._drive(lambda item: item, cases, concurrency=2)
        self.assertFalse(stress._gate(summary)[0])


if __name__ == "__main__":
    unittest.main()
