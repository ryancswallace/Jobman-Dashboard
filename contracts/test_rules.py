import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]


class RuleContractTests(unittest.TestCase):
    def test_rule_projection_excludes_private_authority(self):
        schema = json.loads((ROOT / "api/openapi.json").read_text())["components"]["schemas"]
        public = schema["Rule"]["properties"]
        for key in ("accountId", "directoryId", "principalId", "activation", "boundary", "headCursor", "namespaces"):
            self.assertNotIn(key, public)
        self.assertEqual(public["revision"]["type"], "string")
        self.assertEqual(public["scopes"]["maxItems"], 320)
        self.assertEqual(schema["RulePage"]["properties"]["items"]["maxItems"], 20)
        self.assertEqual(set(schema["RuleInput"]["required"]), set(schema["RuleInput"]["properties"]))

    def test_mutations_keep_owner_and_activation_fields_server_owned(self):
        document = json.loads((ROOT / "api/openapi.json").read_text())
        base = "/api/v1/rules/{ruleId}"
        for path, method in ((base, "put"), (base, "delete"), (base + "/enabled", "put"), (base + "/revalidate", "post")):
            op = document["paths"][path][method]
            headers = {p["name"]: p for p in op["parameters"] if p["in"] == "header"}
            self.assertTrue(headers["If-Match"]["required"])
            self.assertIn("X-CSRF-Token", headers)
        self.assertNotIn("requestBody", document["paths"][base + "/revalidate"]["post"])
        self.assertEqual(set(document["components"]["schemas"]["RuleEnabledInput"]["properties"]), {"enabled"})
        ts = (ROOT / "contracts/typescript/dashboard.generated.ts").read_text()
        swift = (ROOT / "contracts/swift/DashboardAPI.generated.swift").read_text()
        for method in ("rule", "rules", "createRule", "updateRule", "deleteRule", "setRuleEnabled", "revalidateRule"):
            self.assertIn(f"  {method}(", ts)
            self.assertIn(f"public func {method}(", swift)


if __name__ == "__main__":
    unittest.main()
