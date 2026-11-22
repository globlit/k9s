import io
import unittest
import benchcheck


def sample(change="+12.00%", base="1", head="1.12", unit="sec/op"):
    p = "0.500" if change == "~" else "0.001"
    return io.StringIO(f"pkg: example\n,{unit},CI,{unit},CI,vs base,P\nWork-4,{base},1%,{head},1%,{change},p={p} n=10\n")


class BenchCheckTest(unittest.TestCase):
    def test_regression(self):
        for unit in benchcheck.LIMITS:
            row = benchcheck.parse(sample(unit=unit))[0]
            self.assertGreater(row[5], benchcheck.LIMITS[unit])

    def test_improvement_and_noise(self):
        for change in ("-15.00%", "~", "+2.00%"):
            row = benchcheck.parse(sample(change))[0]
            self.assertLessEqual(row[5], benchcheck.LIMITS[row[2]])

    def test_missing_or_invalid(self):
        for data in (io.StringIO(""), sample(base=""), sample(head=""), sample(base="nan"), sample(change="bad")):
            with self.assertRaises(ValueError):
                benchcheck.parse(data)

    def test_statistics_required(self):
        for stats in ("p=0.001 n=6", "p=0.500 n=10", ""):
            data = sample().getvalue().replace("p=0.001 n=10", stats)
            with self.assertRaises(ValueError):
                benchcheck.parse(io.StringIO(data))

    def test_unrelated_metric_does_not_inherit_time_unit(self):
        data = sample().getvalue() + sample(unit="B/s", change="+50.00%").getvalue()
        rows = benchcheck.parse(io.StringIO(data))
        self.assertEqual(1, len(rows))
        self.assertEqual("sec/op", rows[0][2])

    def test_zero_base(self):
        self.assertEqual(float("inf"), benchcheck.parse(sample(change="?", base="0"))[0][5])


if __name__ == "__main__":
    unittest.main()
