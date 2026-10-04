import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]


class DeviceContractTests(unittest.TestCase):
    def test_responses_never_expose_device_authority_or_credentials(self):
        schemas = json.loads((ROOT / "api/openapi.json").read_text())["components"]["schemas"]
        for name in ("Device", "DevicePage", "InstallationState", "DeviceRevocationReceipt"):
            for private in ("accountId", "bindingId", "tokenVersion", "token", "tokenCiphertext", "installationSecret"):
                self.assertNotIn(private, schemas[name]["properties"])
        self.assertEqual(schemas["Device"]["properties"]["revision"]["type"], "string")
        self.assertEqual(schemas["DevicePage"]["properties"]["items"]["maxItems"], 50)
        self.assertEqual(set(schemas["DevicePage"]["properties"]), {"items"})
        for name in ("DeviceBindInput", "DeviceSwitchInput", "DeviceTokenInput", "InstallationProofInput"):
            self.assertTrue(schemas[name]["properties"]["installationSecret"]["writeOnly"])
            self.assertEqual(set(schemas[name]["required"]), set(schemas[name]["properties"]))
        self.assertEqual(set(schemas["DeviceSettingsInput"]["properties"]), {"label", "enabled", "muted"})

    def test_binding_intent_and_startup_refresh_are_distinct(self):
        doc = json.loads((ROOT / "api/openapi.json").read_text())
        base = "/api/v1/devices/{installationId}"
        bind = doc["paths"][base + "/bind"]["post"]
        headers = {p["name"]: p for p in bind["parameters"] if p["in"] == "header"}
        self.assertNotIn("If-None-Match", headers)
        self.assertTrue(headers["If-Match"]["required"])
        reserve = doc["paths"][base + "/revocation-reservations"]["post"]
        self.assertIn("If-None-Match", {p["name"] for p in reserve["parameters"]})
        self.assertEqual(doc["paths"]["/auth/native/device-revocations"]["post"]["security"], [])
        self.assertIn("If-Match", headers)
        for path, method in ((base, "delete"), (base + "/switch", "post"), (base + "/token", "put"), (base + "/settings", "put"), (base + "/detach", "post")):
            headers = {p["name"]: p for p in doc["paths"][path][method]["parameters"] if p["in"] == "header"}
            self.assertTrue(headers["If-Match"]["required"])
            self.assertIn("X-CSRF-Token", headers)
        for path, item in doc["paths"].items():
            if not path.startswith("/api/v1/devices"):
                continue
            for op in item.values():
                self.assertFalse(any(p["in"] == "query" for p in op["parameters"]))
        ts = (ROOT / "contracts/typescript/dashboard.generated.ts").read_text()
        swift = (ROOT / "contracts/swift/DashboardAPI.generated.swift").read_text()
        for method in ("devices", "inspectInstallation", "bindDevice", "switchDeviceBinding", "refreshDeviceToken", "updateDeviceSettings", "removeDevice", "detachInstallation", "reserveDeviceRevocation", "activateDeviceRevocation", "revokeNativeDevice"):
            self.assertIn(f"  {method}(", ts)
            self.assertIn(f"public func {method}(", swift)
        self.assertNotIn("  updateDevice(", ts)


if __name__ == "__main__":
    unittest.main()
