import Foundation

/// The generated client's requests share the same bounded, origin-pinned native transport.
public struct AuthenticatedTransport: DashboardHTTPTransport {
    public let transport: DashboardTransport
    private let token: String
    public init(transport: DashboardTransport, token: String) { self.transport = transport; self.token = token }
    public func send(_ request: DashboardHTTPRequest) async throws -> Data {
        let idempotency = request.headers["Idempotency-Key"].flatMap(UUID.init(uuidString:))
        if request.headers["Idempotency-Key"] != nil, idempotency == nil { throw DashboardError.invalidResponse }
        return try await transport.data(path: request.path,
                                        query: request.query.keys.sorted().map { .init(name: $0, value: request.query[$0]) },
                                        token: token, method: request.method, body: request.body,
                                        revision: request.headers["If-Match"], ifNoneMatch: request.headers["If-None-Match"], idempotencyKey: idempotency)
    }
}

public extension DashboardTransport {
    func bootstrap(token: String) async throws -> Bootstrap {
        let client = DashboardClient(transport: AuthenticatedTransport(transport: self, token: token))
        let wire = try await client.bootstrap()
        return try JSONDecoder().decode(Bootstrap.self, from: JSONEncoder().encode(wire))
    }
}
