import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("profile_tool", Path(__file__).resolve().parents[1] / "cc-switch/profile_tool.py")
profile = importlib.util.module_from_spec(spec)
spec.loader.exec_module(profile)


class ProfileExportTest(unittest.TestCase):
    def test_all_four_defaults_export_and_secret_rejected(self):
        with tempfile.TemporaryDirectory() as location:
            directory = Path(location)
            profile.seed(directory)
            self.assertEqual(len(profile.export(directory)["files"]), 4)
            path = directory / ".claude/settings.json"
            content = json.loads(path.read_text())
            content["env"]["ANTHROPIC_AUTH_TOKEN"] = "test-sensitive-value"
            path.write_text(json.dumps(content))
            with self.assertRaisesRegex(ValueError, "credentials"):
                profile.export(directory)

    def test_external_gateway_and_arbitrary_provider_rejected(self):
        with tempfile.TemporaryDirectory() as location:
            directory = Path(location)
            profile.seed(directory)
            path = directory / ".codex/config.toml"
            path.write_text(path.read_text().replace("capability.internal", "external.invalid"))
            with self.assertRaisesRegex(ValueError, "gateway"):
                profile.export(directory)


if __name__ == "__main__":
    unittest.main()
