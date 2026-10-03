import Testing
@testable import DashboardCore

@Test func graphLayoutIsBoundedAndDoesNotLoseExternalEdgeDisclosure() throws {
    let nodes = (0..<300).map { GraphLayout.Input(id: String(format: "%03d", $0), upstream: $0 == 0 ? ["outside"] : [String(format: "%03d", $0 - 1)]) }
    let layout = try GraphLayout.calculate(nodes)
    #expect(layout.vertices.count == 200)
    #expect(layout.edges.count == 199)
    #expect(layout.omittedNodes == 100)
    #expect(layout.omittedEdges == 1)
    #expect(!layout.containsCycle)
}

@Test func malformedGraphCycleDoesNotHangOrMasqueradeAsReadiness() throws {
    let layout = try GraphLayout.calculate([.init(id: "a", upstream: ["b"]), .init(id: "b", upstream: ["a"])])
    #expect(layout.containsCycle)
    #expect(layout.vertices.count == 2)
    #expect(layout.vertices.allSatisfy { $0.column == 1 })
}
