import datetime as dt
import unittest
from quota_estimate import assign_estimates, weight

TODAY = dt.date(2026, 9, 3)


def member(model="gpt-5.6-terra", **overrides):
    return {"model": model, "input_tokens": 1000, "cached_input_tokens": 500,
            "output_tokens": 100, **overrides}


class EstimateTests(unittest.TestCase):
    def test_cache_is_subset_and_reasoning_not_added(self):
        m = member(reasoning_output_tokens=99)
        self.assertEqual(weight(m, TODAY)[0], 500*50 + 500*5 + 100*300)

    def test_session_denominator_includes_main(self):
        data = [member("gpt-5.6-sol"), member(), member()]
        assign_estimates(data, today=TODAY)
        self.assertAlmostEqual(sum(m["estimated_quota_share"] for m in data), 100)
        self.assertGreater(data[0]["estimated_quota_share"], data[1]["estimated_quota_share"])

    def test_unknown_or_missing_denominator_not_renormalized(self):
        for bad in [member("unknown"), member(cached_input_tokens=None), member(mixed_pricing_context=True),
                    member(cached_input_tokens=1001), member(service_tier="priority")]:
            data = [member(), bad]
            assign_estimates(data, today=TODAY)
            self.assertTrue(all(m["estimated_quota_share"] is None for m in data))

    def test_empty_zero_no_root_and_expired(self):
        assign_estimates([], today=TODAY)
        for options in [{"has_main": False, "today": TODAY}, {"today": dt.date(2027, 1, 1)}]:
            data = [member()]
            assign_estimates(data, **options)
            self.assertIsNone(data[0]["estimated_quota_share"])
        data = [member(input_tokens=0, cached_input_tokens=0, output_tokens=0)]
        assign_estimates(data, today=TODAY)
        self.assertIsNone(data[0]["estimated_quota_share"])

    def test_fast_explicit_standard_assumed(self):
        ordinary, basis = weight(member(), TODAY)
        self.assertIn("Standard assumed", basis)
        self.assertEqual(weight(member(service_tier="fast"), TODAY)[0], ordinary * 2.5)

    def test_does_not_assign_account_percentage(self):
        data = [member(tokens=1100, account_used_percent=80)]
        assign_estimates(data, today=TODAY)
        self.assertEqual(data[0]["estimated_quota_share"], 100)
        self.assertNotIn("quota_share", data[0])


if __name__ == "__main__": unittest.main()
