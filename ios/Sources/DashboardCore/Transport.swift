import Foundation

public enum DashboardError: Error, Equatable, Sendable, LocalizedError {
    case invalidAddress
    case authenticationRequired
    case forbidden
    case authorizationUnavailable
    case notFound
    case sourceUnavailable
    case unsupportedContract
    case cursorExpired
    case targetChanged
    case streamChanged
    case logGap
    case storageUnavailable
    case invalidEvidence
    case redactionUnavailable
    case snapshotChanged
    case revisionConflict
    case rateLimited
    case ruleCapacity
    case deviceCapacity
    case unsupportedOutcome
    case invalidResponse
    case responseTooLarge
    case network
    case server(String)

    public var errorDescription: String? {
        switch self {
        case .invalidAddress: "Enter the HTTPS address supplied by your Dashboard administrator."
        case .authenticationRequired: "Your session has expired. Sign in again to continue."
        case .forbidden: "Your current permissions do not allow this view."
        case .authorizationUnavailable: "Current access cannot be verified. Try again when directory services recover."
        case .notFound: "This item is unavailable or you no longer have access."
        case .sourceUnavailable: "A Control deployment is unavailable. Other deployments may still be usable."
        case .unsupportedContract: "This service version is not compatible with the app. Contact your administrator."
        case .cursorExpired: "This page has expired. Refresh to start a new list."
        case .targetChanged: "The target generation changed. Refresh the target before browsing partitions."
        case .streamChanged: "The log execution changed. Refresh to start a new stream."
        case .logGap: "Log bytes are no longer contiguous. Refresh to start a new stream."
        case .storageUnavailable: "Log storage is unavailable. No empty output has been assumed."
        case .invalidEvidence: "The report could not be validated against its evidence."
        case .redactionUnavailable: "Log-tail reports require an operator-configured redaction policy. Generate a metadata report or contact your administrator."
        case .snapshotChanged: "The source snapshot changed. Request a new report."
        case .revisionConflict: "This request conflicts with the current revision or an earlier request. Refresh and try again."
        case .rateLimited: "Too many requests. Wait briefly before retrying."
        case .deviceCapacity: "Device or retained installation capacity has been reached. Stopping or removing devices remains available; contact your operator."
        case .ruleCapacity: "The alert rule or history capacity has been reached. Stop or remove unused rules, or contact your operator."
        case .unsupportedOutcome: "An outcome in this rule is not supported by the service. Review the selected outcomes."
        case .invalidResponse: "The service returned an unexpected response."
        case .responseTooLarge: "The service response exceeded the app's safe size limit."
        case .network: "Dashboard cannot be reached. Check your private network or VPN connection."
        case .server: "The service could not complete this request. Try again."
        }
    }

    public static func from(code: String, status: Int) -> Self {
        switch code {
        case "unauthenticated": .authenticationRequired
        case "forbidden": .forbidden
        case "authorization_unavailable": .authorizationUnavailable
        case "not_found_or_inaccessible": .notFound
        case "source_unavailable": .sourceUnavailable
        case "unsupported_contract": .unsupportedContract
        case "cursor_expired": .cursorExpired
        case "target_changed": .targetChanged
        case "stream_changed": .streamChanged
        case "log_gap": .logGap
        case "storage_unavailable": .storageUnavailable
        case "invalid_evidence": .invalidEvidence
        case "redaction_unavailable": .redactionUnavailable
        case "snapshot_changed": .snapshotChanged
        case "revision_conflict": .revisionConflict
        case "rate_limited": .rateLimited
        case "rule_capacity": .ruleCapacity
        case "device_capacity": .deviceCapacity
        case "unsupported_outcome": .unsupportedOutcome
        default:
            switch status {
            case 401: .authenticationRequired
            case 403: .forbidden
            case 404: .notFound
            case 409: .cursorExpired
            case 429: .rateLimited
            default: .server(code)
            }
        }
    }
}

public struct DashboardConnection: Equatable, Sendable {
    public let baseURL: URL
    public init(address: String) throws {
        guard let parts = URLComponents(string: address), parts.scheme?.lowercased() == "https",
              let host = parts.host, !host.isEmpty, parts.user == nil, parts.password == nil,
              parts.query == nil, parts.fragment == nil, parts.path.isEmpty || parts.path == "/",
              let url = parts.url else { throw DashboardError.invalidAddress }
        self.baseURL = url
    }
}

/// Ephemeral URLSession + no redirects prevents cookies, persistent API caches and bearer forwarding.
public final class DashboardTransport: Sendable {
    private let connection: DashboardConnection
    public var origin: URL { connection.baseURL }
    private let session: URLSession
    public static let maximumResponseBytes = 4 * 1024 * 1024

    public init(connection: DashboardConnection) {
        self.connection = connection
        let configuration = URLSessionConfiguration.ephemeral
        configuration.urlCache = nil
        configuration.httpCookieStorage = nil
        configuration.httpShouldSetCookies = false
        configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
        configuration.timeoutIntervalForRequest = 15
        configuration.timeoutIntervalForResource = 30
        self.session = URLSession(configuration: configuration, delegate: NoRedirects(), delegateQueue: nil)
    }

    init(connection: DashboardConnection, session: URLSession) {
        self.connection = connection
        self.session = session
    }

    #if DEBUG
    public static func testing(connection: DashboardConnection, session: URLSession) -> DashboardTransport {
        DashboardTransport(connection: connection, session: session)
    }
    #endif

    public func request<T: Decodable & Sendable>(_ type: T.Type = T.self, path: String,
                                               query: [URLQueryItem] = [], token: String?,
                                               method: String = "GET", body: Data? = nil,
                                               revision: String? = nil, idempotencyKey: UUID? = nil) async throws -> T {
        let data = try await data(path: path, query: query, token: token, method: method, body: body, revision: revision, idempotencyKey: idempotencyKey)
        if data.isEmpty, T.self == EmptyResponse.self { return EmptyResponse() as! T }
        do { return try JSONDecoder().decode(T.self, from: data) }
        catch { throw DashboardError.invalidResponse }
    }

    public func data(path: String, query: [URLQueryItem] = [], token: String?, method: String = "GET",
                     body: Data? = nil, revision: String? = nil, ifNoneMatch: String? = nil, idempotencyKey: UUID? = nil) async throws -> Data {
        guard ifNoneMatch == nil || (ifNoneMatch == "*" && revision == nil) else { throw DashboardError.invalidResponse }
        let apiPath = path.hasPrefix("/api/v1/")
        let unbindPath = path == "/auth/native/device-revocations" && method == "POST" && token == nil
        if unbindPath, !query.isEmpty || revision != nil || ifNoneMatch != nil || idempotencyKey != nil { throw DashboardError.invalidAddress }
        guard (apiPath || unbindPath), !path.contains(".."), !path.contains("?"), !path.contains("#"),
              var parts = URLComponents(url: connection.baseURL, resolvingAgainstBaseURL: false) else { throw DashboardError.invalidAddress }
        parts.percentEncodedPath = path
        parts.queryItems = query.isEmpty ? nil : query
        guard let url = parts.url else { throw DashboardError.invalidAddress }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.httpBody = body
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request.setValue("no-store", forHTTPHeaderField: "Cache-Control")
        if let token { request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        if body != nil { request.setValue("application/json", forHTTPHeaderField: "Content-Type") }
        if let revision { request.setValue(revision, forHTTPHeaderField: "If-Match") }
        if let ifNoneMatch { request.setValue(ifNoneMatch, forHTTPHeaderField: "If-None-Match") }
        if let idempotencyKey { request.setValue(idempotencyKey.uuidString, forHTTPHeaderField: "Idempotency-Key") }
        do {
            let (bytes, response) = try await session.bytes(for: request)
            guard let http = response as? HTTPURLResponse else { throw DashboardError.invalidResponse }
            guard response.expectedContentLength <= Self.maximumResponseBytes else { throw DashboardError.responseTooLarge }
            var data = Data()
            for try await byte in bytes {
                guard data.count < Self.maximumResponseBytes else { throw DashboardError.responseTooLarge }
                data.append(byte)
            }
            guard (200..<300).contains(http.statusCode) else {
                let error = try? JSONDecoder().decode(APIErrorEnvelope.self, from: data)
                throw DashboardError.from(code: error?.error?.code ?? error?.code ?? "unknown", status: http.statusCode)
            }
            return data
        } catch is CancellationError { throw CancellationError() }
        catch let error as DashboardError { throw error }
        catch let error as URLError where error.code == .cancelled { throw CancellationError() }
        catch is DecodingError { throw DashboardError.invalidResponse }
        catch { throw DashboardError.network }
    }
}

public struct EmptyResponse: Decodable, Sendable { public init() {} }

private struct APIErrorEnvelope: Decodable {
    struct Detail: Decodable { let code: String }
    let error: Detail?
    let code: String?
}

private final class NoRedirects: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}
