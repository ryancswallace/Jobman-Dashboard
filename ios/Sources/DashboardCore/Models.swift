import Foundation

public struct Bootstrap: Decodable, Sendable {
    public let apiVersion: String
    public let account: Account
    public let deployments: [Deployment]
    public var preferences: Preferences
    public let limits: Limits
    public var namespaces: [NamespaceRef] {
        deployments.flatMap { deployment in deployment.namespaces.map { .init(deploymentId: deployment.id, namespaceId: $0.id) } }
    }
    public var authorizationDeadline: Date? {
        deployments.flatMap(\.namespaces).compactMap { WireDate.parse($0.authorizationExpiresAt) }.min()
    }
    public var authorizationCheckedAt: Date? {
        deployments.flatMap(\.namespaces).compactMap { WireDate.parse($0.authorizationCheckedAt) }.min()
    }
}
public struct Account: Codable, Sendable { public let id: String; public let displayName: String }
public struct Deployment: Codable, Sendable, Identifiable {
    public let id: String
    public let name: String
    public let status: String
    public let namespaces: [Namespace]
}
public struct Namespace: Codable, Sendable, Identifiable {
    public let id: String
    public let name: String
    public let roles: [String]
    public let capabilities: [String]
    public let authorizationVersion: String
    public let authorizationCheckedAt: String
    public let authorizationExpiresAt: String
}
public struct Preferences: Codable, Sendable {
    public var revision: String
    public var timezone: String
    public var appearance: String
    public var refreshSeconds: Int
}
public struct Limits: Codable, Sendable {
    public let defaultPageSize: Int
    public let maxPageSize: Int
    public let logReadBytes: Int
    public let logBufferBytes: Int
    public let graphNodes: Int
    public let graphEdges: Int
}
public struct SourceStatus: Codable, Sendable, Identifiable {
    public let deploymentId: String
    public let namespaceId: String
    public let status: String
    public let asOf: String?
    public let fetchedAt: String
    public let message: String?
    public var id: NamespaceRef { .init(deploymentId: deploymentId, namespaceId: namespaceId) }
}
public struct Page<Item: Decodable & Sendable>: Decodable, Sendable {
    public let items: [Item]
    public let nextCursor: String?
    public let completeness: String
    public let sources: [SourceStatus]
    public let fetchedAt: String
}
public struct Overview: Decodable, Sendable {
    public struct Window: Decodable, Sendable { public let from: String; public let to: String }
    public let active: Int?
    public let awaitingExecution: Int?
    public let running: Int?
    public let evidenceAttention: Int?
    public let missingCompletionTime: Int?
    public let terminal: [String: Int?]
    public let window: Window
    public let sources: [SourceStatus]
    public let completeness: String
    public let fetchedAt: String
}
public struct Job: Codable, Sendable, Identifiable {
    public struct Owner: Codable, Sendable { public let id: String; public let displayName: String?; public let isCurrentUser: Bool }
    public struct Scheduler: Codable, Sendable { public let state: String?; public let jobId: String? }
    public let deploymentId: String
    public let namespaceId: String
    public let id: String
    public let name: String?
    public let targetId: String
    public let backend: String?
    public let revision: String
    public let owner: Owner?
    public let createdAt: String
    public let updatedAt: String
    public let startedAt: String?
    public let completedAt: String?
    public let desiredState: String
    public let phase: String
    public let outcome: String?
    public let confidence: String?
    public let labels: [String: String]
    public let scheduler: Scheduler?
    public let disposition: String?
    public var ref: JobRef { .init(deploymentId: deploymentId, namespaceId: namespaceId, jobId: id) }
    public var title: String { name ?? id }
}
public struct JobDetail: Decodable, Sendable { public let job: Job; public let fetchedAt: String }

public extension DashboardScope {
    func queryItems(authorized: [NamespaceRef]) throws -> [URLQueryItem] {
        if case .all = self { return [] }
        let selected = authorized.filter(contains).sorted {
            $0.deploymentId == $1.deploymentId ? $0.namespaceId < $1.namespaceId : $0.deploymentId < $1.deploymentId
        }
        let data = try JSONEncoder().encode(selected)
        guard let string = String(data: data, encoding: .utf8) else { throw DashboardError.invalidResponse }
        return [.init(name: "scope", value: string)]
    }
}
