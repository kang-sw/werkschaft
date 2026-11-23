import json
from pathlib import Path
import unittest


class WsManifestContractTests(unittest.TestCase):
    def test_ws_launch_configs_list_exact_three_existing_manifests(self):
        root = Path(__file__).resolve().parents[1]
        expected = ["config-manifest.json", "agy-config-manifest.json", "dashboard-config-manifest.json"]
        for config_path, prefix in ((root / ".mcp.json", "./"),
                                    (root / ".claude-plugin/plugin.json", "${CLAUDE_PLUGIN_ROOT}/")):
            with self.subTest(config=str(config_path)):
                config = json.loads(config_path.read_bytes())
                paths = config["mcpServers"]["ws"]["env"]["WS_MCP_CONFIG_MANIFESTS"].split(";")
                self.assertEqual(paths, [prefix + name for name in expected])
                for name in expected:
                    self.assertTrue((root / name).is_file(), name)
