import Foundation
import Testing
@testable import DashboardCore

private final class FixtureURLProtocol: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var respond: ((URLRequest) throws -> (Int, Data))?
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        do {
            let (status, data) = try Self.respond!(request)
            client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type":"application/json"])!, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: data)
            client?.urlProtocolDidFinishLoading(self)
        } catch { client?.urlProtocol(self, didFailWithError: error) }
    }
    override func stopLoading() {}
}

@Suite(.serialized)
struct TransportTests {
    private func client() throws -> DashboardTransport {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [FixtureURLProtocol.self]
        return DashboardTransport(connection: try .init(address: "https://dashboard.example.test"), session: URLSession(configuration: config))
    }

    @Test func nativeCallsUseBearerAndSafeErrorCodes() async throws {
        FixtureURLProtocol.respond = { request in
            #expect(request.value(forHTTPHeaderField: "Authorization") == "Bearer fixture-token")
            #expect(request.value(forHTTPHeaderField: "Cache-Control") == "no-store")
            #expect(request.url?.host == "dashboard.example.test")
            return (503, Data(#"{"code":"authorization_unavailable","message":"Private detail must not be shown"}"#.utf8))
        }
        do {
            let _: EmptyResponse = try await client().request(path: "/api/v1/bootstrap", token: "fixture-token")
            Issue.record("Expected authorization freshness error")
        } catch let error as DashboardError { #expect(error == .authorizationUnavailable) }
    }

    @Test func opaqueUnbindCredentialCanOnlyUseExactUnauthenticatedPath() async throws {
        FixtureURLProtocol.respond = { request in
            #expect(request.value(forHTTPHeaderField: "Authorization") == nil)
            #expect(request.value(forHTTPHeaderField: "Cookie") == nil)
            #expect(request.value(forHTTPHeaderField: "Origin") == nil)
            #expect(request.value(forHTTPHeaderField: "If-Match") == nil)
            return (204, Data())
        }
        let _: EmptyResponse = try await client().request(path: "/auth/native/device-revocations", token: nil, method: "POST", body: Data("{}".utf8))
        do {
            let _: EmptyResponse = try await client().request(path: "/auth/native/device-revocations", token: "wrong", method: "POST")
            Issue.record("Unbind endpoint accepted bearer token")
        } catch let error as DashboardError { #expect(error == .invalidAddress) }
        do {
            _ = try await client().data(path: "/auth/native/device-revocations", token: nil, method: "POST", revision: "1")
            Issue.record("Anonymous revocation accepted a conditional header")
        } catch let error as DashboardError { #expect(error == .invalidAddress) }
    }

    @Test func oversizedResponseIsRejectedBeforeDecode() async throws {
        FixtureURLProtocol.respond = { _ in (200, Data(repeating: 65, count: DashboardTransport.maximumResponseBytes + 1)) }
        do {
            let _: EmptyResponse = try await client().request(path: "/api/v1/bootstrap", token: "fixture-token")
            Issue.record("Oversized response was accepted")
        } catch let error as DashboardError { #expect(error == .responseTooLarge) }
    }

    @Test func generatedReservationPreservesExclusiveConditionalCreationHeader() async throws {
        FixtureURLProtocol.respond = { request in
            #expect(request.httpMethod == "POST")
            #expect(request.value(forHTTPHeaderField: "If-None-Match") == "*")
            #expect(request.value(forHTTPHeaderField: "If-Match") == nil)
            #expect(request.value(forHTTPHeaderField: "Idempotency-Key") == nil)
            return (200, Data(#"{"installationId":"90000000-0000-4000-8000-000000000001","revision":"1","revocationId":"91000000-0000-4000-8000-000000000001","intent":"bind"}"#.utf8))
        }
        let installation = try DeviceInstallation(id: "90000000-0000-4000-8000-000000000001", randomBytes: Data(repeating: 1, count: 32))
        let capability = try DeviceRevocationCapability(id: "91000000-0000-4000-8000-000000000001", randomBytes: Data(repeating: 2, count: 32))
        let transport = try client()
        let generated = DashboardClient(transport: AuthenticatedTransport(transport: transport, token: "fixture-token"))
        let receipt = try await generated.reserveDeviceRevocation(installationId: installation.id, headers: .init(ifNoneMatch: "*"), body: installation.revocationInput(capability))
        try receipt.validate(installationID: installation.id, capability: capability, intent: "bind")
        do {
            _ = try await transport.data(path: "/api/v1/devices/test/revocation-reservations", token: "fixture-token", method: "POST", revision: "1", ifNoneMatch: "*")
            Issue.record("Mutually exclusive revision and creation headers were accepted")
        } catch let error as DashboardError { #expect(error == .invalidResponse) }
    }
}
