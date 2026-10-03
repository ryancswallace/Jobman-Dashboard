import Foundation

/// IDs are opaque. Namespace names and job IDs alone never identify a resource.
public struct NamespaceRef: Codable, Hashable, Sendable, Identifiable {
    public let deploymentId: String
    public let namespaceId: String
    public var id: Self { self }
    public init(deploymentId: String, namespaceId: String) {
        self.deploymentId = deploymentId
        self.namespaceId = namespaceId
    }
}

public struct JobRef: Codable, Hashable, Sendable, Identifiable {
    public let deploymentId: String
    public let namespaceId: String
    public let jobId: String
    public var id: Self { self }
    public var namespace: NamespaceRef { .init(deploymentId: deploymentId, namespaceId: namespaceId) }
    public init(deploymentId: String, namespaceId: String, jobId: String) {
        self.deploymentId = deploymentId
        self.namespaceId = namespaceId
        self.jobId = jobId
    }
}

public enum DashboardScope: Hashable, Sendable {
    case all
    case deployments(Set<String>)
    case namespace(NamespaceRef)

    public var queryItems: [URLQueryItem] {
        switch self {
        case .all: return []
        case .deployments(let ids): return ids.sorted().map { .init(name: "deploymentId", value: $0) }
        case .namespace(let ref): return [.init(name: "deploymentId", value: ref.deploymentId), .init(name: "namespaceId", value: ref.namespaceId)]
        }
    }
    public func contains(_ ref: NamespaceRef) -> Bool {
        switch self {
        case .all: true
        case .deployments(let ids): ids.contains(ref.deploymentId)
        case .namespace(let selected): selected == ref
        }
    }
}

/// All path components are encoded independently, so an ID cannot inject another route.
public enum APIPath {
    public static func component(_ value: String) -> String {
        value.addingPercentEncoding(withAllowedCharacters: .alphanumerics.union(.init(charactersIn: "-._~"))) ?? ""
    }
    public static func job(_ ref: JobRef) -> String {
        "/api/v1/deployments/\(component(ref.deploymentId))/namespaces/\(component(ref.namespaceId))/jobs/\(component(ref.jobId))"
    }
}

public enum DashboardRoute: Hashable, Sendable {
    case job(JobRef)
    case inbox(String)

    /// Content links carry no authority. Callers still authenticate and fetch the destination.
    public init?(url: URL) {
        guard url.scheme?.lowercased() == "jobman-dashboard", url.user == nil, url.password == nil,
              url.port == nil, url.query == nil, url.fragment == nil else { return nil }
        let parts = url.path.split(separator: "/", omittingEmptySubsequences: false).dropFirst().map(String.init)
        guard parts.allSatisfy({ !$0.isEmpty && $0.utf8.count <= 256 && !$0.contains("/") && !$0.contains("\\") && $0 != "." && $0 != ".." }) else { return nil }
        switch url.host {
        case "job" where parts.count == 3:
            self = .job(.init(deploymentId: parts[0], namespaceId: parts[1], jobId: parts[2]))
        case "inbox" where parts.count == 1:
            self = .inbox(parts[0])
        default: return nil
        }
    }

    public var url: URL? {
        switch self {
        case .job(let ref): URL(string: "jobman-dashboard://job/\(APIPath.component(ref.deploymentId))/\(APIPath.component(ref.namespaceId))/\(APIPath.component(ref.jobId))")
        case .inbox(let id): URL(string: "jobman-dashboard://inbox/\(APIPath.component(id))")
        }
    }
}
