import copy
import contextlib
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("generate_contracts", ROOT / "scripts/generate-contracts.py")
generator = importlib.util.module_from_spec(spec)
spec.loader.exec_module(generator)


class ContractGeneratorTests(unittest.TestCase):
    def setUp(self):
        self.raw = (ROOT / "api/openapi.json").read_bytes()
        self.document = json.loads(self.raw)

    def generate(self, document=None):
        return generator.generate(document or self.document, hashlib.sha256(self.raw).hexdigest())

    def test_checked_in_artifacts_match_and_generation_is_deterministic(self):
        first = self.generate()
        self.assertEqual(first, self.generate())
        for path, content in first.items():
            self.assertEqual((ROOT / path).read_text(), content, path)

    def test_rejects_unsupported_composition_instead_of_ignoring_it(self):
        self.document["components"]["schemas"]["Job"]["oneOf"] = []
        with self.assertRaisesRegex(generator.ContractError, "unsupported schema keywords"):
            self.generate()

    def test_rejects_missing_reference(self):
        self.document["components"]["schemas"]["JobDetail"]["properties"]["job"] = {"$ref": "#/components/schemas/Missing"}
        with self.assertRaisesRegex(generator.ContractError, "unknown schema"):
            self.generate()

    def test_rejects_duplicate_parameter_or_operation(self):
        operation = self.document["paths"]["/api/v1/jobs"]["get"]
        operation["parameters"].append(copy.deepcopy(operation["parameters"][0]))
        with self.assertRaisesRegex(generator.ContractError, "duplicate query parameter"):
            self.generate()

    def test_rejects_missing_path_parameter(self):
        operation = self.document["paths"]["/api/v1/deployments/{deploymentId}/namespaces/{namespaceId}/jobs/{jobId}"]["get"]
        operation["parameters"] = operation["parameters"][1:]
        with self.assertRaisesRegex(generator.ContractError, "path parameter mismatch"):
            self.generate()

    def test_generated_clients_preserve_nullable_and_unknown_enum_semantics(self):
        generated = self.generate()
        ts = generated["contracts/typescript/dashboard.generated.ts"]
        swift = generated["contracts/swift/DashboardAPI.generated.swift"]
        self.assertIn('"phase": string;', ts)
        self.assertIn('"active": number | null;', ts)
        self.assertIn('container.decode(Int64?.self, forKey: .`active`)', swift)
        self.assertIn('public var `revision`: String', swift)
        self.assertIn('encodeURIComponent(String(parameters.path["jobId"]))', ts)
        self.assertIn('segment(jobId)', swift)

    def test_schema_addition_changes_both_typed_clients(self):
        self.document["components"]["schemas"]["Job"]["properties"]["futureField"] = {"type": "string"}
        generated = self.generate()
        self.assertIn('"futureField"?: string;', generated["contracts/typescript/dashboard.generated.ts"])
        self.assertIn('public var `futureField`: String?', generated["contracts/swift/DashboardAPI.generated.swift"])

    def test_check_detects_stale_schema_without_writing(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            schema = root / "openapi.json"
            schema.write_bytes(self.raw)
            with patch.object(generator, "ROOT", root), patch.object(generator, "SCHEMA", schema), contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
                with patch("sys.argv", ["generate-contracts.py"]):
                    self.assertEqual(generator.main(), 0)
                before = {path: path.read_bytes() for path in root.rglob("*") if path.is_file() and path != schema}
                self.document["components"]["schemas"]["Job"]["properties"]["futureField"] = {"type": "string"}
                schema.write_text(json.dumps(self.document))
                with patch("sys.argv", ["generate-contracts.py", "--check"]):
                    self.assertEqual(generator.main(), 1)
                self.assertEqual(before, {path: path.read_bytes() for path in before})


if __name__ == "__main__":
    unittest.main()
