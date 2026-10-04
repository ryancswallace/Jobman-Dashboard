#if DEBUG
import Foundation

/// Synthetic retained metadata only. Kept out of Release; pages are generated lazily.
enum NativeGraphFixtures {
    static let nodeCount = 10_000
    static let edgeCount = 100_000
    static let graphPath = "/api/v1/deployments/east/namespaces/research/workloads/graph/graph-ceiling"
    static var enabled: Bool { ProcessInfo.processInfo.arguments.contains("--dashboard-graph-ceiling-fixtures") }
    static func nodeID(_ index: Int) -> String { String(format: "76000000-0000-4000-8000-%012d", index + 1) }
    static func nodeIndex(_ id: String) -> Int? {
        guard let suffix = id.split(separator: "-").last, let number = Int(suffix), (1...nodeCount).contains(number), nodeID(number - 1) == id else { return nil }
        return number - 1
    }
    // 9,999 root-star edges, followed by 90,001 forward edges; no retained graph array.
    static func outgoing(_ index: Int) -> [Int] {
        if index == 0 { return Array(1..<nodeCount) }
        if (1...9_000).contains(index) { return Array((index + 1)...(index + 10)) }
        return index == 9_001 ? [9_002] : []
    }
    static func incoming(_ index: Int) -> [Int] {
        guard index > 0 else { return [] }
        var values = [0]
        if index > 1 {
            let first = max(1, index - 10), last = min(9_000, index - 1)
            if first <= last { values += Array(first...last) }
        }
        if index == 9_002 { values.append(9_001) }
        return values
    }
    struct Pair: Equatable { let from: Int; let to: Int }
    static func dependencies(_ index: Int, direction: String) -> [Pair] {
        let parents = direction == "outgoing" ? [] : incoming(index).map { Pair(from: $0, to: index) }
        let children = direction == "incoming" ? [] : outgoing(index).map { Pair(from: index, to: $0) }
        return parents + children
    }
    static func response(path: String, query: [URLQueryItem]) throws -> [String: Any]? {
        guard path == "/api/v1/workloads/graph" || path == graphPath || path.hasPrefix(graphPath + "/") else { return nil }
        let grouped = Dictionary(grouping: query, by: \.name)
        guard grouped.values.allSatisfy({ $0.count == 1 }) else { throw FixtureError.invalidQuery }
        func value(_ name: String) -> String? { grouped[name]?.first?.value }
        func limit(_ name: String, fallback: Int, maximum: Int) throws -> Int {
            guard let raw = value(name) else { return fallback }
            guard let result = Int(raw), result > 0, result <= maximum else { throw FixtureError.invalidQuery }
            return result
        }
        let timestamp = ISO8601DateFormatter().string(from: Date())
        let sources: [[String: Any]] = [["deploymentId": "east", "namespaceId": "research", "status": "available", "asOf": timestamp, "fetchedAt": timestamp]]
        func envelope() -> [String: Any] { ["completeness": "complete", "sources": sources, "fetchedAt": timestamp] }
        let workload: [String: Any] = ["id": "graph-ceiling", "kind": "graph", "name": "Synthetic ceiling graph", "deploymentId": "east", "namespaceId": "research", "createdAt": timestamp, "revision": "1", "asOf": timestamp, "totalChildren": String(nodeCount), "phase": "accepted", "counts": ["active": "0", "terminal": "0", "success": "0", "failure": "0", "cancelled": "0", "waiting": String(nodeCount), "skipped": "0", "blocked": "0"], "concurrency": "1", "unsatisfiedPolicy": "blocked"]
        func child(_ index: Int) -> [String: Any] {
            let id = nodeID(index)
            let name = String(format: "node-%05d", index)
            let job: [String: Any] = ["deploymentId": "east", "namespaceId": "research", "id": id, "name": name, "targetId": "synthetic-no-executor", "revision": "1", "createdAt": timestamp, "updatedAt": timestamp, "desiredState": "run", "phase": "accepted", "confidence": "", "group": ["graphId": "graph-ceiling", "graphIndex": index], "labels": ["fixture": "native-graph-ceiling"]]
            return ["id": id, "name": name, "index": String(index), "job": job, "dependencyCounts": ["total": String(incoming(index).count), "satisfied": "0", "waiting": String(incoming(index).count), "unsatisfied": "0"]]
        }
        func edge(_ pair: Pair) -> [String: Any] {
            ["from": String(format: "node-%05d", pair.from), "to": String(format: "node-%05d", pair.to), "fromJobId": nodeID(pair.from), "toJobId": nodeID(pair.to), "predicate": "success", "outcomes": [String](), "state": "waiting", "upstreamPhase": "accepted"]
        }
        func offset(binding: String, total: Int) throws -> Int {
            guard let cursor = value("cursor") else { return 0 }
            guard let bytes = Data(base64Encoded: cursor), let decoded = String(data: bytes, encoding: .utf8), decoded.hasPrefix(binding + ":"), let number = Int(decoded.dropFirst(binding.count + 1)), number > 0, number < total else { throw FixtureError.invalidCursor }
            return number
        }
        func cursor(binding: String, position: Int) -> String { Data("\(binding):\(position)".utf8).base64EncodedString() }
        if path == "/api/v1/workloads/graph" {
            var result = envelope(); result["items"] = [workload]; result["total"] = "1"
            result["totals"] = [["deploymentId": "east", "namespaceId": "research", "total": "1", "asOf": timestamp]]
            return result
        }
        if path.hasSuffix("/dependencies") || path.hasSuffix("/neighborhood") {
            guard let node = value("nodeId"), let index = nodeIndex(node) else { throw FixtureError.invalidQuery }
            if path.hasSuffix("/dependencies") {
                let direction = value("direction") ?? ""
                guard ["incoming", "outgoing", ""].contains(direction) else { throw FixtureError.invalidQuery }
                let pairs = dependencies(index, direction: direction), count = try limit("limit", fallback: 100, maximum: 500)
                let binding = graphPath + ":edges:" + node + ":" + direction
                let start = try offset(binding: binding, total: pairs.count), end = min(start + count, pairs.count)
                var result = envelope(); result["items"] = pairs[start..<end].map(edge); result["total"] = String(pairs.count)
                if end < pairs.count { result["nextCursor"] = cursor(binding: binding, position: end) }
                return result
            }
            let maxNodes = try limit("maxNodes", fallback: 50, maximum: 200), maxEdges = try limit("maxEdges", fallback: 100, maximum: 500)
            let adjacent = Set(incoming(index) + outgoing(index)).subtracting([index]).sorted()
            let complete = Set([index] + adjacent)
            let nodes = ([index] + adjacent.prefix(maxNodes - 1)).sorted(), selected = Set(nodes)
            var pairs: [Pair] = [], totalEdges = 0
            for from in complete.sorted() {
                for to in outgoing(from) where complete.contains(to) {
                    totalEdges += 1
                    if selected.contains(from) && selected.contains(to) && pairs.count < maxEdges { pairs.append(Pair(from: from, to: to)) }
                }
            }
            var result = envelope(); result["centerId"] = node; result["nodes"] = nodes.map(child); result["edges"] = pairs.map(edge)
            result["totalNodes"] = String(complete.count); result["totalEdges"] = String(totalEdges)
            result["omittedNodes"] = String(complete.count - nodes.count); result["omittedEdges"] = String(totalEdges - pairs.count)
            return result
        }
        guard path == graphPath || path == graphPath + "/children" else { throw FixtureError.invalidQuery }
        let count = try limit("limit", fallback: 50, maximum: 200), binding = graphPath + ":children"
        let start = try offset(binding: binding, total: nodeCount), end = min(start + count, nodeCount)
        var result = envelope(); result["total"] = String(nodeCount)
        if path == graphPath { result["workload"] = workload; result["children"] = (start..<end).map(child) }
        else { result["items"] = (start..<end).map(child) }
        if end < nodeCount { result["nextCursor"] = cursor(binding: binding, position: end) }
        return result
    }
    enum FixtureError: Error { case invalidQuery, invalidCursor }
}
#endif
