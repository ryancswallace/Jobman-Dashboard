#!/usr/bin/env python3
"""Generate transport-only TypeScript and Swift from the authored OpenAPI.

Uses Python stdlib only. Deliberately supports a validated, small schema subset;
unsupported composition/content/parameter constructs fail rather than disappear.
Generated code does not authenticate or replace server/domain validation.
"""
from __future__ import annotations
import argparse
import hashlib
import json
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1]
SCHEMA = ROOT / "api/openapi.json"
SCHEMA_KEYS = {"$ref", "type", "properties", "required", "additionalProperties", "items", "enum", "minimum", "maximum", "pattern", "nullable", "format", "description", "example", "minLength", "maxLength", "minItems", "maxItems", "default", "readOnly", "writeOnly"}
METHODS = {"get", "post", "put", "patch", "delete", "head"}
IDENTIFIER = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")

class ContractError(ValueError):
    pass

def require(condition, message):
    if not condition:
        raise ContractError(message)

def ref_name(schema):
    ref = schema["$ref"]
    require(ref.startswith("#/components/schemas/"), f"External reference unsupported: {ref}")
    return ref.rsplit("/", 1)[1]

def validate_schema(schema, models, location, root=False):
    require(isinstance(schema, dict), f"{location}: schema must be an object")
    unknown = set(schema) - SCHEMA_KEYS
    require(not unknown, f"{location}: unsupported schema keywords {sorted(unknown)}")
    if "$ref" in schema:
        name = ref_name(schema)
        require(name in models, f"{location}: unknown schema {name}")
        require(set(schema) <= {"$ref", "description", "nullable"}, f"{location}: unsupported reference siblings")
        return
    kind = schema.get("type")
    require(kind in {"object", "array", "string", "integer", "number", "boolean"}, f"{location}: unsupported type {kind}")
    if kind == "object":
        props = schema.get("properties", {})
        require(isinstance(props, dict), f"{location}: properties must be an object")
        require(root or not props, f"{location}: inline object properties need a named component")
        require(set(schema.get("required", [])) <= set(props), f"{location}: required field absent from properties")
        additional = schema.get("additionalProperties", False)
        require(additional is False or isinstance(additional, dict), f"{location}: untyped additional properties unsupported")
        require(not props or additional is False, f"{location}: mixed fixed and dictionary fields unsupported")
        for name, child in props.items():
            require(IDENTIFIER.fullmatch(name), f"{location}: invalid property identifier {name}")
            validate_schema(child, models, f"{location}.{name}")
        if isinstance(additional, dict):
            validate_schema(additional, models, f"{location}[]")
    elif kind == "array":
        require("items" in schema, f"{location}: array requires items")
        validate_schema(schema["items"], models, f"{location}[]")
    if "nullable" in schema:
        require(isinstance(schema["nullable"], bool), f"{location}: nullable must be boolean")
    if "enum" in schema:
        require(kind in {"string", "integer", "number", "boolean"}, f"{location}: complex enums unsupported")

def json_schema(content, location):
    require(set(content) == {"application/json"}, f"{location}: only application/json content is supported")
    require(set(content["application/json"]) <= {"schema", "example", "examples"}, f"{location}: unsupported media fields")
    return content["application/json"]["schema"]

def operations(document):
    result = []
    ids = set()
    models = document["components"]["schemas"]
    for path, item in sorted(document["paths"].items()):
        require(path.startswith("/api/v1/"), f"Unsupported route {path}")
        require(set(item) <= METHODS, f"{path}: unsupported path-level fields")
        for method, operation in sorted(item.items()):
            op_id = operation.get("operationId", "")
            require(IDENTIFIER.fullmatch(op_id), f"{path}: invalid operationId")
            require(op_id not in ids, f"Duplicate operationId {op_id}")
            ids.add(op_id)
            require(set(operation) <= {"operationId", "parameters", "requestBody", "responses", "summary", "description", "tags", "security", "deprecated"}, f"{op_id}: unsupported operation fields")
            parameters = operation.get("parameters", [])
            seen = set()
            for parameter in parameters:
                require(set(parameter) <= {"in", "name", "required", "schema", "description", "deprecated"}, f"{op_id}: unsupported parameter encoding")
                where, name = parameter["in"], parameter["name"]
                require(where in {"path", "query", "header"}, f"{op_id}: unsupported parameter location")
                require((where, name) not in seen, f"{op_id}: duplicate {where} parameter {name}")
                seen.add((where, name))
                if where == "path":
                    require(parameter.get("required") is True, f"{op_id}: path parameters must be required")
                validate_schema(parameter["schema"], models, f"{op_id}.{name}")
                require(parameter["schema"].get("type") in {"string", "integer", "number", "boolean"}, f"{op_id}: only scalar parameters supported")
            path_names = set(re.findall(r"\{([^}]+)\}", path))
            require(path_names == {p["name"] for p in parameters if p["in"] == "path"}, f"{op_id}: path parameter mismatch")
            successes = [(code, value) for code, value in operation["responses"].items() if code.startswith("2")]
            require(len(successes) == 1, f"{op_id}: exactly one success response supported")
            status, response = successes[0]
            response_schema = None if status == "204" else json_schema(response.get("content", {}), op_id)
            if response_schema:
                validate_schema(response_schema, models, f"{op_id}.response")
            body = operation.get("requestBody")
            if body:
                require(body.get("required") is True, f"{op_id}: request bodies must be required in this generator")
                body = json_schema(body["content"], op_id)
                validate_schema(body, models, f"{op_id}.body")
            result.append(dict(id=op_id, path=path, method=method.upper(), parameters=parameters, response=response_schema, body=body))
    return result

def ts_type(schema):
    if "$ref" in schema:
        result = ref_name(schema)
    else:
        kind = schema["type"]
        if kind == "object":
            additional = schema.get("additionalProperties", False)
            result = f"Record<string, {ts_type(additional)}>" if isinstance(additional, dict) else "Record<string, never>"
        elif kind == "array":
            result = f"Array<{ts_type(schema['items'])}>"
        else:
            result = {"string": "string", "integer": "number", "number": "number", "boolean": "boolean"}[kind]
    return result + (" | null" if schema.get("nullable") else "")

def swift_type(schema):
    if "$ref" in schema:
        result = "DashboardAPI." + ref_name(schema)
    else:
        kind = schema["type"]
        if kind == "object":
            additional = schema.get("additionalProperties", False)
            require(isinstance(additional, dict), "Swift requires typed dictionary or named fixed-field object")
            result = f"[String: {swift_type(additional)}]"
        elif kind == "array":
            result = f"[{swift_type(schema['items'])}]"
        else:
            result = {"string": "String", "integer": "Int64", "number": "Double", "boolean": "Bool"}[kind]
    return result + ("?" if schema.get("nullable") else "")

def upper(value):
    return value[0].upper() + value[1:]

def identifier(value):
    parts = value.split("-")
    result = parts[0][0].lower() + parts[0][1:] + "".join(upper(p) for p in parts[1:])
    require(IDENTIFIER.fullmatch(result), f"Unsupported generated identifier {value}")
    return result

def typescript(document, ops, digest):
    lines = [f"// Generated by scripts/generate-contracts.py; do not edit.\n// OpenAPI SHA-256: {digest}\n// Unknown source enums remain strings. Auth, bounds and runtime validation belong to the injected transport and server.\n"]
    for name, schema in sorted(document["components"]["schemas"].items()):
        if schema.get("type") == "object" and schema.get("properties"):
            lines.append(f"export interface {name} {{")
            required = set(schema.get("required", []))
            for key, value in schema["properties"].items():
                lines.append(f"  {json.dumps(key)}{'' if key in required else '?'}: {ts_type(value)};")
            lines.append("}\n")
        else:
            lines.append(f"export type {name} = {ts_type(schema)};\n")
    lines.extend(["export interface DashboardRequest {", "  method: string;", "  path: string;", "  query?: Record<string, string | number | boolean | undefined>;", "  headers?: Record<string, string | undefined>;", "  body?: unknown;", "  signal?: AbortSignal;", "}", "export type DashboardTransport = <T>(request: DashboardRequest) => Promise<T>;", ""])
    for op in ops:
        name = upper(op["id"])
        lines.append(f"export interface {name}Parameters {{")
        for where in ["path", "query", "header"]:
            params = [p for p in op["parameters"] if p["in"] == where]
            if not params:
                continue
            field = "headers" if where == "header" else where
            required = any(p.get("required") for p in params)
            lines.append(f"  {field}{'' if required else '?'}: {{")
            lines.extend(f"    {json.dumps(p['name'])}{'' if p.get('required') else '?'}: {ts_type(p['schema'])};" for p in params)
            lines.append("  };")
        if op["body"]:
            lines.append(f"  body: {ts_type(op['body'])};")
        lines.append("  signal?: AbortSignal;\n}\n")
    lines.append("export class DashboardClient {\n  constructor(private readonly transport: DashboardTransport) {}")
    for op in ops:
        required = bool(op["body"]) or any(p.get("required") for p in op["parameters"])
        default = "" if required else " = {}"
        response = ts_type(op["response"]) if op["response"] else "void"
        expression = "`" + re.sub(r"\{([^}]+)\}", lambda m: "${encodeURIComponent(String(parameters.path[" + json.dumps(m[1]) + "]))}", op["path"]) + "`"
        lines.append(f"  {op['id']}(parameters: {upper(op['id'])}Parameters{default}): Promise<{response}> {{")
        fields = [f"method: {json.dumps(op['method'])}", f"path: {expression}", "signal: parameters.signal"]
        for where in ["query", "header"]:
            if any(p["in"] == where for p in op["parameters"]):
                field = "headers" if where == "header" else where
                fields.append(f"{field}: parameters.{field}")
        if op["body"]:
            fields.append("body: parameters.body")
        lines.append(f"    return this.transport<{response}>({{ {', '.join(fields)} }});\n  }}")
    lines.append("}\n")
    return "\n".join(lines)

def swift_struct(name, props, required, indent="  ", codable=True):
    lines = [f"{indent}public struct {name}: {'Codable, ' if codable else ''}Sendable {{"]
    fields = []
    for key, schema in props.items():
        field = key if codable else identifier(key)
        base = swift_type(schema)
        typ = base if key in required or base.endswith("?") else base + "?"
        fields.append((key, field, typ, schema, key in required))
        lines.append(f"{indent}  public var `{field}`: {typ}")
    params = [f"{field}: {typ}" + ("" if required_field else " = nil") for _, field, typ, _, required_field in fields]
    lines.append(f"{indent}  public init({', '.join(params)}) {{")
    lines.extend(f"{indent}    self.`{field}` = `{field}`" for _, field, *_ in fields)
    lines.append(f"{indent}  }}")
    if codable:
        lines.append(f"{indent}  private enum CodingKeys: String, CodingKey {{")
        lines.extend(f"{indent}    case `{key}`" for key, *_ in fields)
        lines.append(f"{indent}  }}")
        lines.append(f"{indent}  public init(from decoder: any Decoder) throws {{")
        lines.append(f"{indent}    let container = try decoder.container(keyedBy: CodingKeys.self)")
        for key, field, typ, schema, required_field in fields:
            method = "decode" if required_field else "decodeIfPresent"
            decode_type = typ if required_field else typ.removesuffix("?")
            lines.append(f"{indent}    self.`{field}` = try container.{method}({decode_type}.self, forKey: .`{key}`)")
        lines.append(f"{indent}  }}")
    lines.append(f"{indent}}}\n")
    return lines

def swift(document, ops, digest):
    lines = [f"// Generated by scripts/generate-contracts.py; do not edit.\n// OpenAPI SHA-256: {digest}\n// Dates and wide integers retain their wire strings; unknown enums remain strings.\nimport Foundation\n", "public enum DashboardAPI {"]
    for name, schema in sorted(document["components"]["schemas"].items()):
        if schema.get("type") == "object" and schema.get("properties"):
            lines += swift_struct(name, schema["properties"], set(schema.get("required", [])))
        else:
            lines.append(f"  public typealias {name} = {swift_type(schema)}")
    lines.append("}\n")
    for op in ops:
        for where in ["query", "header"]:
            params = [p for p in op["parameters"] if p["in"] == where]
            if params:
                lines += swift_struct(upper(op["id"]) + ("Headers" if where == "header" else "Query"), {p["name"]: p["schema"] for p in params}, {p["name"] for p in params if p.get("required")}, indent="", codable=False)
    lines.extend(["public struct DashboardHTTPRequest: Sendable {", "  public let method: String", "  public let path: String", "  public let query: [String: String]", "  public let headers: [String: String]", "  public let body: Data?", "}", "", "/// Transport must enforce the approved origin, authentication, no-store, cancellation, response bounds and non-2xx errors.", "public protocol DashboardHTTPTransport: Sendable {", "  func send(_ request: DashboardHTTPRequest) async throws -> Data", "}", "", "public struct DashboardClient: Sendable {", "  private let transport: any DashboardHTTPTransport", "  public init(transport: any DashboardHTTPTransport) { self.transport = transport }", "  private func segment(_ value: String) -> String {", "    value.addingPercentEncoding(withAllowedCharacters: CharacterSet(charactersIn: \"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~\"))!", "  }"])
    for op in ops:
        name = upper(op["id"])
        args = [f"{p['name']}: {swift_type(p['schema'])}" for p in op["parameters"] if p["in"] == "path"]
        for where in ["query", "header"]:
            params = [p for p in op["parameters"] if p["in"] == where]
            if params:
                label = "headers" if where == "header" else "query"
                typ = name + ("Headers" if where == "header" else "Query")
                args.append(f"{label}: {typ}" + ("" if any(p.get("required") for p in params) else " = .init()"))
        if op["body"]:
            args.append("body: " + swift_type(op["body"]))
        response = swift_type(op["response"]) if op["response"] else "Void"
        lines.append(f"  public func {op['id']}({', '.join(args)}) async throws -> {response} {{")
        for where in ["query", "header"]:
            params = [p for p in op["parameters"] if p["in"] == where]
            label = "headers" if where == "header" else "query"
            lines.append(f"    {'var' if params else 'let'} {label}Values: [String: String] = [:]")
            for p in params:
                name_literal = json.dumps(p["name"])
                field = identifier(p["name"])
                if p.get("required"):
                    lines.append(f"    {label}Values[{name_literal}] = String(describing: {label}.`{field}`)")
                else:
                    lines.append(f"    if let value = {label}.`{field}` {{ {label}Values[{name_literal}] = String(describing: value) }}")
        path_literal = json.dumps(op["path"])
        path_literal = re.sub(r"\{([^}]+)\}", lambda m: "\\(segment(" + m[1] + "))", path_literal)
        body = "try JSONEncoder().encode(body)" if op["body"] else "nil"
        lines.append(f"    let request = DashboardHTTPRequest(method: {json.dumps(op['method'])}, path: {path_literal}, query: queryValues, headers: headersValues, body: {body})")
        if op["response"]:
            lines.append(f"    let data = try await transport.send(request)\n    return try JSONDecoder().decode({response}.self, from: data)")
        else:
            lines.append("    _ = try await transport.send(request)")
        lines.append("  }")
    lines.append("}\n")
    return "\n".join(lines)

def generate(document, digest):
    require(document.get("openapi") == "3.0.3", "Only OpenAPI 3.0.3 is supported")
    models = document["components"]["schemas"]
    for name, schema in models.items():
        require(IDENTIFIER.fullmatch(name), f"Invalid model identifier {name}")
        validate_schema(schema, models, name, root=True)
    ops = operations(document)
    manifest = {"apiVersion": document["info"]["version"], "generator": "scripts/generate-contracts.py", "openapiSha256": digest, "operations": [op["id"] for op in ops], "schemaCount": len(models)}
    return {"contracts/typescript/dashboard.generated.ts": typescript(document, ops, digest), "contracts/swift/DashboardAPI.generated.swift": swift(document, ops, digest), "contracts/manifest.json": json.dumps(manifest, indent=2, sort_keys=True) + "\n"}

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="Fail if checked-in generated files differ")
    args = parser.parse_args()
    raw = SCHEMA.read_bytes()
    try:
        files = generate(json.loads(raw), hashlib.sha256(raw).hexdigest())
    except (ContractError, KeyError, TypeError) as error:
        print(f"Contract generation failed: {error}", file=sys.stderr)
        return 1
    stale = []
    for relative, content in files.items():
        path = ROOT / relative
        if args.check:
            if not path.exists() or path.read_text() != content:
                stale.append(relative)
        else:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)
    if stale:
        print("Generated contracts differ: " + ", ".join(stale), file=sys.stderr)
        return 1
    print(f"{'Verified' if args.check else 'Generated'} {len(files)} contract artifacts")
    return 0

if __name__ == "__main__":
    sys.exit(main())
