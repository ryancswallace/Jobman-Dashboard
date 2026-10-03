import AuthenticationServices
import CryptoKit
import DashboardCore
import Foundation
import Security
import UIKit

@MainActor
final class NativeAuthentication: NSObject, ASWebAuthenticationPresentationContextProviding {
    private var browserSession: ASWebAuthenticationSession?
    private var accessToken: String?
    private var expiresAt = Date.distantPast
    private var configuration: NativeOAuthConfiguration?
    private var credentialKey: String?
    private var refreshTask: Task<String, Error>?
    private var generation = UUID()
    private let network: URLSession = {
        let config = URLSessionConfiguration.ephemeral
        config.urlCache = nil
        config.httpCookieStorage = nil
        config.httpShouldSetCookies = false
        config.timeoutIntervalForRequest = 20
        config.timeoutIntervalForResource = 30
        return URLSession(configuration: config, delegate: RejectAuthRedirects(), delegateQueue: nil)
    }()

    private struct Registration: Decodable {
        let issuer: String; let clientId: String; let audience: String
        let scopes: [String]; let redirectURI: String
    }
    private struct Discovery: Decodable {
        let issuer: String; let authorization_endpoint: String; let token_endpoint: String
        let code_challenge_methods_supported: [String]?
    }

    func signIn(connection: DashboardConnection) async throws -> String {
        generation = UUID()
        let attemptGeneration = generation
        let registration: Registration = try await json(URLRequest(url: connection.baseURL.appendingPathComponent("auth/native/config")))
        guard let issuer = URL(string: registration.issuer), issuer.scheme == "https", issuer.user == nil,
              issuer.password == nil, issuer.query == nil, issuer.fragment == nil else { throw OAuthError.invalidConfiguration }
        let discovery: Discovery = try await json(URLRequest(url: issuer.appendingPathComponent(".well-known/openid-configuration")))
        guard discovery.issuer == registration.issuer,
              discovery.code_challenge_methods_supported?.contains("S256") != false else { throw OAuthError.invalidConfiguration }
        let config = NativeOAuthConfiguration(issuer: registration.issuer, authorizationEndpoint: discovery.authorization_endpoint,
                                              tokenEndpoint: discovery.token_endpoint, clientId: registration.clientId,
                                              redirectURI: registration.redirectURI, scopes: registration.scopes, resource: registration.audience)
        try config.validate()
        configuration = config
        credentialKey = connection.baseURL.absoluteString + "|" + config.issuer + "|" + config.clientId
        let attempt = try OAuthAttempt(configuration: config)
        guard let url = attempt.authorizationURL else { throw OAuthError.invalidConfiguration }
        let callback = try await authenticate(url: url)
        let code = try attempt.authorizationCode(callback: callback)
        let tokens: OAuthTokens = try await json(attempt.tokenRequest(code: code))
        guard attemptGeneration == generation, !Task.isCancelled else { throw CancellationError() }
        try accept(tokens)
        return tokens.accessToken
    }

    func token() async throws -> String {
        if let accessToken, expiresAt.timeIntervalSinceNow > 30 { return accessToken }
        if let refreshTask { return try await refreshTask.value }
        let task = Task { @MainActor in try await self.refresh() }
        refreshTask = task
        defer { refreshTask = nil }
        return try await task.value
    }

    private func refresh() async throws -> String {
        let attemptGeneration = generation
        guard let config = configuration, let key = credentialKey,
              let refresh = try KeychainCredential.read(key: key), let url = URL(string: config.tokenEndpoint) else { throw DashboardError.authenticationRequired }
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
        request.httpBody = OAuthAttempt.form(["grant_type": "refresh_token", "refresh_token": refresh, "client_id": config.clientId])
        let tokens: OAuthTokens = try await json(request)
        guard attemptGeneration == generation, !Task.isCancelled else { throw CancellationError() }
        try accept(tokens)
        return tokens.accessToken
    }

    private func accept(_ tokens: OAuthTokens) throws {
        try tokens.validate()
        if let refresh = tokens.refreshToken, let key = credentialKey { try KeychainCredential.write(refresh, key: key) }
        accessToken = tokens.accessToken
        expiresAt = Date().addingTimeInterval(TimeInterval(tokens.expiresIn))
    }

    func signOut() {
        generation = UUID()
        browserSession?.cancel()
        browserSession = nil
        refreshTask?.cancel()
        refreshTask = nil
        if let key = credentialKey { KeychainCredential.delete(key: key) }
        accessToken = nil
        expiresAt = .distantPast
        configuration = nil
        credentialKey = nil
        network.getAllTasks { tasks in tasks.forEach { $0.cancel() } }
    }

    private func authenticate(url: URL) async throws -> URL {
        try await withCheckedThrowingContinuation { continuation in
            let session = ASWebAuthenticationSession(url: url, callbackURLScheme: "jobman-dashboard-auth") { callback, error in
                if let callback { continuation.resume(returning: callback) }
                else { continuation.resume(throwing: error ?? OAuthError.rejected) }
            }
            session.presentationContextProvider = self
            session.prefersEphemeralWebBrowserSession = true
            browserSession = session
            guard session.start() else { continuation.resume(throwing: OAuthError.rejected); return }
        }
    }

    private func json<T: Decodable>(_ request: URLRequest) async throws -> T {
        let (bytes, response) = try await network.bytes(for: request)
        guard let response = response as? HTTPURLResponse, (200..<300).contains(response.statusCode) else { throw OAuthError.rejected }
        var data = Data()
        for try await byte in bytes {
            guard data.count < 128 * 1024 else { throw DashboardError.responseTooLarge }
            data.append(byte)
        }
        return try JSONDecoder().decode(T.self, from: data)
    }

    func presentationAnchor(for session: ASWebAuthenticationSession) -> ASPresentationAnchor {
        UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.flatMap(\.windows).first(where: \.isKeyWindow) ?? ASPresentationAnchor()
    }
}

private final class RejectAuthRedirects: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) { completionHandler(nil) }
}

private enum KeychainCredential {
    private static func query(key: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: "JobmanDashboard.OAuth",
         kSecAttrAccount as String: Data(SHA256.hash(data: Data(key.utf8))).base64EncodedString()]
    }
    static func write(_ value: String, key: String) throws {
        delete(key: key)
        var attributes = query(key: key)
        attributes[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        attributes[kSecValueData as String] = Data(value.utf8)
        guard SecItemAdd(attributes as CFDictionary, nil) == errSecSuccess else { throw OAuthError.invalidTokens }
    }
    static func read(key: String) throws -> String? {
        var attributes = query(key: key)
        attributes[kSecReturnData as String] = true
        attributes[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(attributes as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = result as? Data else { throw DashboardError.authenticationRequired }
        return String(data: data, encoding: .utf8)
    }
    static func delete(key: String) { SecItemDelete(query(key: key) as CFDictionary) }
}
