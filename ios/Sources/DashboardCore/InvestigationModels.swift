import Foundation

public struct Workload: Codable, Sendable, Identifiable {
    public let resourceId: String; public let name: String?; public let kind: String
    public let deploymentId: String; public let namespaceId: String; public let createdAt: String
    public let totalChildren: String; public let counts: [String: String]
    public let concurrency: String?; public let failurePolicy: String?; public let arrayId: String?
    public var id: String { [deploymentId, namespaceId, kind, resourceId].map(APIPath.component).joined(separator: "/") }
    public var path: String { "/api/v1/deployments/\(APIPath.component(deploymentId))/namespaces/\(APIPath.component(namespaceId))/workloads/\(APIPath.component(kind))/\(APIPath.component(resourceId))" }
    enum CodingKeys: String, CodingKey {
        case resourceId = "id", name, kind, deploymentId, namespaceId, createdAt, totalChildren, counts, concurrency, failurePolicy, arrayId
    }
}
public struct WorkloadChild: Decodable, Sendable, Identifiable {
    public struct Dependency: Decodable, Sendable { public let upstreamNodeId: String; public let predicate: String; public let status: String }
    public let id: String; public let job: Job; public let readiness: String?; public let disposition: String?
    public let taskIndex: String?; public let dependencies: [Dependency]?
}
public struct WorkloadDetail: Decodable, Sendable {
    public let workload: Workload; public let children: [WorkloadChild]; public let nextCursor: String?
    public let omittedNodes: String?; public let omittedEdges: String?
}
public struct Target: Decodable, Sendable, Identifiable {
    public let targetId: String; public let deploymentId: String; public let namespaceId: String
    public let name: String; public let state: String; public let provider: String?; public let backend: String?
    public let generation: String?; public let capabilities: [String]
    public var id: String { [deploymentId, namespaceId, targetId].map(APIPath.component).joined(separator: "/") }
}
public struct Artifact: Decodable, Sendable, Identifiable {
    public let id: String; public let name: String; public let sizeBytes: String; public let checksum: String?
    public let publishedAt: String?; public let availability: String
}
public struct LogChunk: Decodable, Sendable {
    public let text: String?
    public let bytesBase64: String?
    public let stream: String; public let runId: String?; public let executionId: String?
    public let startOffset: String; public let endOffset: String; public let nextCursor: String?
    public let state: String; public let truncated: Bool?; public let capturedAt: String?
}
public struct DiagnosisReport: Decodable, Sendable, Identifiable {
    public struct Citation: Decodable, Sendable, Identifiable {
        public let id: String; public let label: String; public let text: String?; public let startOffset: String?; public let endOffset: String?
    }
    public struct Finding: Decodable, Sendable, Identifiable {
        public let id: String; public let severity: String; public let title: String; public let explanation: String
        public let confidence: Double?; public let confidenceBasis: String?; public let citations: [Citation]
        public let suggestions: [String]?; public let generated: Bool?
    }
    public let id: String; public let state: String; public let createdAt: String?; public let sourceRevision: String?
    public let evidenceId: String?; public let analysisEvidenceId: String?; public let engineVersion: String?
    public let disclosure: String?; public let findings: [Finding]; public let missingEvidence: [String]
    public let warnings: [String]?; public let retryAdvice: String?; public let message: String?
}
public struct AlertRule: Codable, Sendable, Identifiable {
    public struct Activation: Codable, Sendable { public let deploymentId: String; public let status: String }
    public var id: String; public var revision: String; public var name: String; public var enabled: Bool
    public var scope: String; public var namespaces: [NamespaceRef]; public var jobs: [JobRef]
    public var outcomeMode: String; public var outcomes: [String]; public var activation: [Activation]?
    public init(name: String = "", scope: String = "namespace_jobs", namespaces: [NamespaceRef] = [], jobs: [JobRef] = []) {
        id = ""; revision = ""; self.name = name; enabled = true; self.scope = scope; self.namespaces = namespaces; self.jobs = jobs
        outcomeMode = "selected"; outcomes = ["failure", "timed_out", "aborted", "lost"]
    }
    public var input: Input { Input(name: name, enabled: enabled, scope: scope, namespaces: namespaces, jobs: jobs, outcomeMode: outcomeMode, outcomes: outcomes) }
    public struct Input: Encodable, Sendable {
        public let name: String; public let enabled: Bool; public let scope: String
        public let namespaces: [NamespaceRef]; public let jobs: [JobRef]
        public let outcomeMode: String; public let outcomes: [String]
    }
}
public struct InboxItem: Decodable, Sendable, Identifiable {
    public let id: String; public let job: JobRef; public let outcome: String; public let eventAt: String; public let createdAt: String
    public let read: Bool; public let matchedRules: [String]; public let deliveryStatus: String?
}
public struct Device: Decodable, Sendable, Identifiable {
    public let id: String; public let name: String; public let platform: String?; public let enabled: Bool
    public let permission: String?; public let lastSeenAt: String?
}
