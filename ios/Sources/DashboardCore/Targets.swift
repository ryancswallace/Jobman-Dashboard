import Foundation

public typealias Target = DashboardAPI.Target
extension DashboardAPI.Target: Identifiable {
    public var id: String { [deploymentId, namespaceId, targetId].map(APIPath.component).joined(separator: "/") }
    public var path: String { "/api/v1/deployments/\(APIPath.component(deploymentId))/namespaces/\(APIPath.component(namespaceId))/targets/\(APIPath.component(targetId))" }
    public func validate() throws {
        guard !targetId.isEmpty, !generation.id.isEmpty, !asOf.isEmpty,
              let total = UInt64(generation.partitionCount), total >= generation.partitions.count,
              generation.partitionsTruncated == (total > generation.partitions.count),
              generation.partitions.count <= 200, generation.capabilities.count <= 1024,
              Set(generation.partitions.map(\.name)).count == generation.partitions.count else { throw DashboardError.invalidResponse }
    }
}
public struct TargetDetail: Decodable, Sendable {
    public let target: Target; public let sources: [SourceStatus]; public let completeness: String; public let fetchedAt: String
    public func validate(expected: Target) throws {
        try target.validate()
        guard target.id == expected.id, sources.allSatisfy({ $0.deploymentId == expected.deploymentId && $0.namespaceId == expected.namespaceId }) else { throw DashboardError.invalidResponse }
    }
}
public struct TargetPartitionPage: Decodable, Sendable {
    public let targetId: String; public let generationId: String; public let items: [DashboardAPI.TargetPartition]
    public let total: String; public let nextCursor: String?; public let sources: [SourceStatus]
    public let completeness: String; public let fetchedAt: String
    public func validate(target: Target) throws {
        guard targetId == target.targetId, generationId == target.generation.id, items.count <= 200,
              let count = UInt64(total), count >= items.count,
              Set(items.map(\.name)).count == items.count,
              items.map(\.name) == items.map(\.name).sorted(),
              sources.allSatisfy({ $0.deploymentId == target.deploymentId && $0.namespaceId == target.namespaceId }) else { throw DashboardError.invalidResponse }
    }
}
