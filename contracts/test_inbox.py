import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]


class InboxContractTests(unittest.TestCase):
    def test_history_projection_and_exact_counts(self):
        schema = json.loads((ROOT / "api/openapi.json").read_text())["components"]["schemas"]
        item = schema["InboxItem"]["properties"]
        for private in ("accountId", "ownerPrincipalId", "activationId", "directoryId", "token", "path"):
            self.assertNotIn(private, item)
            self.assertNotIn(private, schema["InboxMatch"]["properties"])
        self.assertEqual(schema["InboxItemPage"]["properties"]["unreadCount"]["type"], "string")
        self.assertEqual(schema["InboxMatch"]["properties"]["revision"]["type"], "string")
        self.assertEqual(item["matchedRules"]["maxItems"], 100)
        self.assertEqual(schema["InboxItemPage"]["properties"]["items"]["maxItems"], 50)
        self.assertEqual(set(schema["ReadUpdate"]["properties"]), {"read"})
        self.assertEqual(schema["ReadUpdate"]["required"], ["read"])

    def test_routes_and_generated_clients(self):
        document = json.loads((ROOT / "api/openapi.json").read_text())
        parameters = {p["name"]: p for p in document["paths"]["/api/v1/inbox"]["get"]["parameters"]}
        self.assertEqual(set(parameters), {"scope", "cursor", "limit", "unread"})
        self.assertEqual(parameters["limit"]["schema"]["maximum"], 50)
        detail = document["paths"]["/api/v1/inbox/{inboxId}"]
        self.assertEqual(set(detail), {"get", "patch"})
        self.assertIn("X-CSRF-Token", [p["name"] for p in detail["patch"]["parameters"]])
        for operation in ("inbox", "inboxItem", "updateInbox"):
            self.assertIn(f"  {operation}(", (ROOT / "contracts/typescript/dashboard.generated.ts").read_text())
            self.assertIn(f"public func {operation}(", (ROOT / "contracts/swift/DashboardAPI.generated.swift").read_text())


if __name__ == "__main__":
    unittest.main()
