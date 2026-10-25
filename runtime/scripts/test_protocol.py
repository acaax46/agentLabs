import unittest
from verify_opensandbox import parse_events


class ExecdStreamTest(unittest.TestCase):
    def test_real_release_format_and_standard_sse(self):
        records = '{"type":"stdout","text":"ok\\n"}\n\n{"type":"execution_complete","execution_time":7}\n\n'
        self.assertEqual(parse_events(records)[0]["text"], "ok\n")
        self.assertEqual(parse_events('data: {"type":"execution_complete"}\n\n')[0]["type"], "execution_complete")

    def test_incomplete_and_error_streams_cannot_succeed(self):
        for raw in ('', '{"type":"stdout","text":"partial"}', '{"type":"error","text":"failure"}\n{"type":"execution_complete"}'):
            with self.assertRaises(RuntimeError):
                parse_events(raw)


if __name__ == "__main__":
    unittest.main()
