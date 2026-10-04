#if DEBUG
import Foundation
import Testing
@testable import DashboardCore

private func graphData(_ suffix: String = "", _ query: [URLQueryItem] = []) throws -> Data {
    let response = try NativeGraphFixtures.response(path: NativeGraphFixtures.graphPath + suffix, query: query)
    let value = try #require(response)
    let data = try JSONSerialization.data(withJSONObject: value)
    #expect(data.count < 2 * 1_024 * 1_024)
    return data
}

@Test func ceilingFixtureHasExactUniqueForwardTopology() throws {
    var pairs: Set<String> = [], incoming = Array(repeating: 0, count: 10_000)
    for from in 0..<10_000 {
        #expect(NativeGraphFixtures.nodeIndex(NativeGraphFixtures.nodeID(from)) == from)
        for to in NativeGraphFixtures.outgoing(from) {
            #expect(from < to && to < 10_000)
            #expect(pairs.insert("\(from):\(to)").inserted)
            incoming[to] += 1
        }
    }
    #expect(pairs.count == 100_000)
    for node in 0..<10_000 { #expect(NativeGraphFixtures.incoming(node).count == incoming[node]) }
    #expect(NativeGraphFixtures.nodeIndex("76000000-0000-4000-8000-000000000000") == nil)
    #expect(NativeGraphFixtures.nodeIndex("arbitrary-000000000001") == nil)
}

@Test func ceilingFixtureAllNodePagesDecodeAndReplaceWithExactProvenance() throws {
    let decoder = JSONDecoder()
    let first = try decoder.decode(WorkloadDetail.self, from: graphData())
    #expect(first.total == "10000" && first.children.count == 50)
    #expect(first.workload.phase == "accepted" && first.workload.unsatisfiedPolicy == "blocked")
    #expect(first.workload.counts == ["active": "0", "terminal": "0", "success": "0", "failure": "0", "cancelled": "0", "waiting": "10000", "skipped": "0", "blocked": "0"])
    try first.workload.validate(children: first.children, sources: first.sources)
    var ids = Set(first.children.map(\.id)), cursor = first.nextCursor, pages = 1
    while let token = cursor {
        let page = try decoder.decode(Page<WorkloadChild>.self, from: graphData("/children", [.init(name: "limit", value: "50"), .init(name: "cursor", value: token)]))
        try first.workload.validate(children: page.items, sources: page.sources)
        #expect(page.total == "10000" && page.items.count == 50)
        for node in page.items {
            #expect(ids.insert(node.id).inserted)
            #expect(node.job.currentRun == nil && node.job.phase == "accepted")
        }
        pages += 1; cursor = page.nextCursor
        #expect(pages <= 200)
    }
    #expect(pages == 200 && ids.count == 10_000)
}

@Test func ceilingFixtureRootDependencyPagesAndMaximumNeighborhoodUseRealContracts() throws {
    let decoder = JSONDecoder(), node = NativeGraphFixtures.nodeID(0)
    let detail = try decoder.decode(WorkloadDetail.self, from: graphData())
    var cursor: String?, ids: Set<[String]> = [], pages = 0
    repeat {
        var query: [URLQueryItem] = [.init(name: "nodeId", value: node), .init(name: "direction", value: "outgoing"), .init(name: "limit", value: "100")]
        if let cursor { query.append(.init(name: "cursor", value: cursor)) }
        let page = try decoder.decode(Page<GraphEdge>.self, from: graphData("/dependencies", query))
        try detail.workload.validate(edges: page.items, node: node, direction: "outgoing", sources: page.sources)
        #expect(page.total == "9999" && page.items.count <= 100)
        for edge in page.items { #expect(ids.insert(edge.id).inserted); #expect(edge.state == "waiting" && edge.predicate == "success" && edge.outcomes.isEmpty && edge.upstreamPhase == "accepted") }
        cursor = page.nextCursor; pages += 1
        #expect(pages <= 100)
    } while cursor != nil
    #expect(pages == 100 && ids.count == 9_999)
    let defaults = try decoder.decode(GraphNeighborhood.self, from: graphData("/neighborhood", [.init(name: "nodeId", value: node)]))
    #expect(defaults.nodes.count == 50 && defaults.edges.count == 100)
    let graph = try decoder.decode(GraphNeighborhood.self, from: graphData("/neighborhood", [.init(name: "nodeId", value: node), .init(name: "maxNodes", value: "200"), .init(name: "maxEdges", value: "500")]))
    try graph.validate(workload: detail.workload, center: node)
    #expect(graph.nodes.count == 200 && graph.edges.count == 500)
    #expect(graph.totalNodes == "10000" && graph.totalEdges == "100000")
    #expect(graph.omittedNodes == "9800" && graph.omittedEdges == "99500")
    let layout = try GraphLayout.calculate(graph.nodes.map { child in .init(id: child.id, upstream: graph.edges.filter { $0.toJobId == child.id }.map(\.fromJobId)) })
    #expect(layout.vertices.count == 200 && layout.edges.count == 500 && !layout.containsCycle)
    #expect(graph.nodes.first?.readiness == nil && graph.nodes.first?.disposition == nil)
    #expect(graph.nodes.first?.dependencyCounts == ["total": "0", "satisfied": "0", "waiting": "0", "unsatisfied": "0"])
}

@Test func ceilingFixtureNonRootNeighborhoodCountsAreInducedNotGlobal() throws {
    let decoder = JSONDecoder(), detail = try decoder.decode(WorkloadDetail.self, from: graphData())
    // Independent oracle: node1 sees 0...11, a complete forward DAG (12 choose2).
    for (index, nodes, edges) in [(1, 12, 66), (9_999, 2, 1)] {
        let node = NativeGraphFixtures.nodeID(index)
        let graph = try decoder.decode(GraphNeighborhood.self, from: graphData("/neighborhood", [.init(name: "nodeId", value: node), .init(name: "maxNodes", value: "200"), .init(name: "maxEdges", value: "500")]))
        try graph.validate(workload: detail.workload, center: node)
        #expect(graph.totalNodes == String(nodes) && graph.totalEdges == String(edges))
        #expect(graph.nodes.count == nodes && graph.edges.count == edges)
        #expect(graph.omittedNodes == "0" && graph.omittedEdges == "0")
        #expect(graph.nodes.allSatisfy { $0.readiness == nil && $0.disposition == nil && $0.job.phase == "accepted" && $0.job.currentRun == nil })
        #expect(graph.edges.allSatisfy { $0.predicate == "success" && $0.state == "waiting" && $0.upstreamPhase == "accepted" && $0.outcomes.isEmpty })
        if index == 1 {
            var expected: [[String]] = []
            for from in 0..<12 { for to in (from + 1)..<12 { expected.append([NativeGraphFixtures.nodeID(from), NativeGraphFixtures.nodeID(to)]) } }
            #expect(graph.edges.map(\.id) == expected)
            #expect(graph.nodes.map(\.index) == (0..<12).map { String($0) })
        }
    }
    let partial = try decoder.decode(GraphNeighborhood.self, from: graphData("/neighborhood", [.init(name: "nodeId", value: NativeGraphFixtures.nodeID(1)), .init(name: "maxNodes", value: "3"), .init(name: "maxEdges", value: "2")]))
    #expect(partial.nodes.map(\.id) == (0..<3).map(NativeGraphFixtures.nodeID))
    #expect(partial.totalNodes == "12" && partial.totalEdges == "66" && partial.omittedNodes == "9" && partial.omittedEdges == "64")
    let both = try decoder.decode(Page<GraphEdge>.self, from: graphData("/dependencies", [.init(name: "nodeId", value: NativeGraphFixtures.nodeID(1)), .init(name: "direction", value: "")]))
    #expect(both.total == "11" && both.items.count == 11)
}

@Test func ceilingFixtureDependencyHistoryKeeps64ReplayPagesAndExplicitRestart() throws {
    let decoder = JSONDecoder(), node = NativeGraphFixtures.nodeID(0)
    let detail = try decoder.decode(WorkloadDetail.self, from: graphData())
    func load(_ cursor: String?) throws -> Page<GraphEdge> {
        var query: [URLQueryItem] = [.init(name: "nodeId", value: node), .init(name: "direction", value: "outgoing"), .init(name: "limit", value: "100")]
        if let cursor { query.append(.init(name: "cursor", value: cursor)) }
        let page = try decoder.decode(Page<GraphEdge>.self, from: graphData("/dependencies", query))
        try detail.workload.validate(edges: page.items, node: node, direction: "outgoing", sources: page.sources)
        return page
    }
    var history = InboxPageHistory(), current: String?, page = try load(nil), pages = 1
    while let next = page.nextCursor {
        let replacement = try load(next)
        history.record(current); current = next; page = replacement; pages += 1
        #expect(history.count <= 64 && page.items.count <= 100 && pages <= 100)
    }
    #expect(pages == 100 && history.count == 64 && history.discardedPages == 35)
    // A cancelled/failed previous read discards only the tentative copy.
    var cancelled = history; _ = cancelled.previous()
    #expect(history.count == 64 && cancelled.count == 63)
    for index in stride(from: 98, through: 35, by: -1) {
        var retained = history
        let previous = retained.previous(), replacement = try load(previous)
        #expect(replacement.items.first?.toJobId == NativeGraphFixtures.nodeID(index * 100 + 1))
        history = retained; current = previous; page = replacement
    }
    #expect(!history.canGoBack && history.discardedPages == 35 && current != nil)
    // Refresh explicitly starts a new traversal and clears the old window.
    page = try load(nil); current = nil; history.reset()
    #expect(page.items.first?.toJobId == NativeGraphFixtures.nodeID(1))
    #expect(history.count == 0 && history.discardedPages == 0 && !history.canGoBack)
}

@Test func ceilingFixtureRejectsCrossQueryCursorsAndOversizedBounds() throws {
    let node = NativeGraphFixtures.nodeID(0)
    let first = try JSONDecoder().decode(WorkloadDetail.self, from: graphData())
    let childrenCursor = try #require(first.nextCursor)
    #expect(throws: NativeGraphFixtures.FixtureError.self) {
        try graphData("/dependencies", [.init(name: "nodeId", value: node), .init(name: "direction", value: "outgoing"), .init(name: "cursor", value: childrenCursor)])
    }
    let page = try JSONDecoder().decode(Page<GraphEdge>.self, from: graphData("/dependencies", [.init(name: "nodeId", value: node), .init(name: "direction", value: "outgoing")]))
    let edgeCursor = try #require(page.nextCursor)
    for query in [
        [URLQueryItem(name: "nodeId", value: node), .init(name: "direction", value: "incoming"), .init(name: "cursor", value: edgeCursor)],
        [.init(name: "nodeId", value: NativeGraphFixtures.nodeID(1)), .init(name: "direction", value: "outgoing"), .init(name: "cursor", value: edgeCursor)],
        [.init(name: "nodeId", value: node), .init(name: "limit", value: "501")],
        [.init(name: "nodeId", value: node), .init(name: "nodeId", value: node)]
    ] { #expect(throws: NativeGraphFixtures.FixtureError.self) { try graphData("/dependencies", query) } }
    #expect(throws: NativeGraphFixtures.FixtureError.self) { try graphData("/neighborhood", [.init(name: "nodeId", value: node), .init(name: "maxEdges", value: "501")]) }
}

@Test func ceilingFixtureEveryDependencyDecodesWithCompleteExactTotals() throws {
    let decoder = JSONDecoder(), detail = try decoder.decode(WorkloadDetail.self, from: graphData())
    var unique: Set<[String]> = []
    for index in 0..<10_000 {
        let node = NativeGraphFixtures.nodeID(index)
        var cursor: String?, count = 0, sourceTotal: String?
        repeat {
            var query: [URLQueryItem] = [.init(name: "nodeId", value: node), .init(name: "direction", value: "outgoing"), .init(name: "limit", value: "100")]
            if let cursor { query.append(.init(name: "cursor", value: cursor)) }
            let page = try decoder.decode(Page<GraphEdge>.self, from: graphData("/dependencies", query))
            try detail.workload.validate(edges: page.items, node: node, direction: "outgoing", sources: page.sources)
            #expect(page.items.count <= 100)
            for edge in page.items { #expect(unique.insert(edge.id).inserted) }
            count += page.items.count; cursor = page.nextCursor
            if let sourceTotal { #expect(page.total == sourceTotal) } else { sourceTotal = page.total }
            #expect(count <= 9_999)
        } while cursor != nil
        #expect(sourceTotal == String(count))
    }
    #expect(unique.count == 100_000)
}
#endif
