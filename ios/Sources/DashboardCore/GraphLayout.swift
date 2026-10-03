import Foundation

public struct GraphLayout: Sendable {
    public struct Input: Hashable, Sendable {
        public let id: String
        public let upstream: [String]
        public init(id: String, upstream: [String]) { self.id = id; self.upstream = upstream }
    }
    public struct Vertex: Sendable, Identifiable {
        public let id: String; public let column: Int; public let row: Int
    }
    public struct Edge: Sendable { public let from: String; public let to: String }
    public let vertices: [Vertex]
    public let edges: [Edge]
    public let omittedNodes: Int
    public let omittedEdges: Int
    public let containsCycle: Bool

    /// Positions are presentation only: readiness/predicates always come from Control.
    public static func calculate(_ input: [Input], maxNodes: Int = 200, maxEdges: Int = 500) throws -> Self {
        let nodeLimit = min(max(0, maxNodes), 200)
        let edgeLimit = min(max(0, maxEdges), 500)
        var seen: Set<String> = []
        let unique = input.filter { seen.insert($0.id).inserted }.sorted { $0.id < $1.id }
        let nodes = Array(unique.prefix(nodeLimit))
        let ids = Set(nodes.map(\.id))
        var edges: [Edge] = []
        var omittedEdges = 0
        var indegree = Dictionary(uniqueKeysWithValues: nodes.map { ($0.id, 0) })
        var outgoing: [String: [String]] = [:]
        for node in nodes {
            try Task.checkCancellation()
            for upstream in Set(node.upstream).sorted() {
                guard ids.contains(upstream), edges.count < edgeLimit else { omittedEdges += 1; continue }
                edges.append(.init(from: upstream, to: node.id))
                outgoing[upstream, default: []].append(node.id)
                indegree[node.id, default: 0] += 1
            }
        }
        var ready = indegree.filter { $0.value == 0 }.map(\.key).sorted()
        var columns: [String: Int] = [:]
        var visited: Set<String> = []
        while !ready.isEmpty {
            try Task.checkCancellation()
            let node = ready.removeFirst()
            visited.insert(node)
            for next in outgoing[node, default: []].sorted() {
                columns[next] = max(columns[next, default: 0], columns[node, default: 0] + 1)
                indegree[next, default: 0] -= 1
                if indegree[next] == 0 { ready.append(next); ready.sort() }
            }
        }
        let cycle = visited.count < nodes.count
        // A malformed cycle never hangs layout or fabricates an execution result.
        let fallbackColumn = (columns.values.max() ?? 0) + 1
        var rows: [Int: Int] = [:]
        let vertices = nodes.map { node in
            let column = visited.contains(node.id) ? columns[node.id, default: 0] : fallbackColumn
            let row = rows[column, default: 0]
            rows[column] = row + 1
            return Vertex(id: node.id, column: column, row: row)
        }
        return Self(vertices: vertices, edges: edges, omittedNodes: unique.count - nodes.count, omittedEdges: omittedEdges, containsCycle: cycle)
    }
}
