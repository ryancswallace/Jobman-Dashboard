import Foundation
import CryptoKit
import Security

public struct NativeOAuthConfiguration: Codable, Sendable {
    public let issuer: String
    public let authorizationEndpoint: String
    public let tokenEndpoint: String
    public let clientId: String
    public let redirectURI: String
    public let scopes: [String]
    public let resource: String?

    public init(issuer: String, authorizationEndpoint: String, tokenEndpoint: String, clientId: String,
                redirectURI: String, scopes: [String], resource: String? = nil) {
        self.issuer = issuer; self.authorizationEndpoint = authorizationEndpoint; self.tokenEndpoint = tokenEndpoint
        self.clientId = clientId; self.redirectURI = redirectURI; self.scopes = scopes; self.resource = resource
    }

    public func validate() throws {
        guard let issuerURL = URL(string: issuer), let issuerHost = issuerURL.host else { throw OAuthError.invalidConfiguration }
        for value in [issuer, authorizationEndpoint, tokenEndpoint] {
            guard let url = URL(string: value), url.scheme == "https", url.host != nil,
                  url.user == nil, url.password == nil, url.fragment == nil, url.query == nil,
                  url.host?.lowercased() == issuerHost.lowercased(), (url.port ?? 443) == (issuerURL.port ?? 443)
            else { throw OAuthError.invalidConfiguration }
        }
        guard !clientId.isEmpty, clientId.utf8.count <= 512,
              !clientId.unicodeScalars.contains(where: CharacterSet.controlCharacters.contains),
              !scopes.isEmpty, scopes.count <= 64, Set(scopes).count == scopes.count,
              scopes.contains("openid"), scopes.allSatisfy({ !$0.isEmpty && $0.utf8.count <= 256 && $0.rangeOfCharacter(from: .whitespacesAndNewlines.union(.controlCharacters)) == nil }),
              redirectURI == "jobman-dashboard-auth://callback",
              resource == nil || (resource!.utf8.count <= 512 && !resource!.isEmpty && resource!.rangeOfCharacter(from: .controlCharacters) == nil)
        else { throw OAuthError.invalidConfiguration }
    }
}

public enum OAuthError: Error, Equatable, LocalizedError {
    case invalidConfiguration, randomGeneration, invalidCallback, rejected, invalidTokens
    public var errorDescription: String? {
        switch self {
        case .invalidConfiguration: "The organization's native sign-in configuration is incomplete or incompatible."
        case .randomGeneration: "Secure sign-in could not be started."
        case .invalidCallback: "The sign-in response did not match this sign-in attempt."
        case .rejected: "The identity service did not complete sign-in."
        case .invalidTokens: "The identity service did not issue a usable API access token."
        }
    }
}

public struct OAuthAttempt: Sendable {
    public let configuration: NativeOAuthConfiguration
    public let verifier: String
    public let state: String
    public let nonce: String

    public init(configuration: NativeOAuthConfiguration) throws {
        try configuration.validate()
        self.configuration = configuration
        verifier = try Self.random()
        state = try Self.random()
        nonce = try Self.random()
    }

    private static func random() throws -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        guard SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes) == errSecSuccess else { throw OAuthError.randomGeneration }
        return Data(bytes).base64URLEncoded
    }

    public static func challenge(verifier: String) -> String { Data(SHA256.hash(data: Data(verifier.utf8))).base64URLEncoded }

    public var authorizationURL: URL? {
        guard var parts = URLComponents(string: configuration.authorizationEndpoint) else { return nil }
        parts.queryItems = [
            .init(name: "response_type", value: "code"), .init(name: "client_id", value: configuration.clientId),
            .init(name: "redirect_uri", value: configuration.redirectURI), .init(name: "scope", value: configuration.scopes.joined(separator: " ")),
            .init(name: "state", value: state), .init(name: "nonce", value: nonce),
            .init(name: "code_challenge", value: Self.challenge(verifier: verifier)), .init(name: "code_challenge_method", value: "S256")
        ]
        if let resource = configuration.resource { parts.queryItems?.append(.init(name: "resource", value: resource)) }
        return parts.url
    }

    public func authorizationCode(callback: URL) throws -> String {
        guard let parts = URLComponents(url: callback, resolvingAgainstBaseURL: false),
              let expected = URLComponents(string: configuration.redirectURI),
              parts.scheme == expected.scheme, parts.host == expected.host, parts.port == expected.port,
              parts.path == expected.path, parts.user == nil, parts.password == nil, parts.fragment == nil else { throw OAuthError.invalidCallback }
        let items = parts.queryItems ?? []
        let states = items.filter { $0.name == "state" }
        guard states.count == 1, states[0].value == state else { throw OAuthError.invalidCallback }
        guard !items.contains(where: { $0.name == "error" }) else { throw OAuthError.rejected }
        let codes = items.filter { $0.name == "code" }
        guard codes.count == 1, let code = codes[0].value, !code.isEmpty, code.utf8.count <= 4096,
              code.rangeOfCharacter(from: .controlCharacters) == nil else { throw OAuthError.invalidCallback }
        return code
    }

    public func tokenRequest(code: String) throws -> URLRequest {
        try Self.request(configuration: configuration, values: [
            "grant_type": "authorization_code", "code": code, "client_id": configuration.clientId,
            "redirect_uri": configuration.redirectURI, "code_verifier": verifier
        ])
    }

    public static func refreshRequest(configuration: NativeOAuthConfiguration, refreshToken: String) throws -> URLRequest {
        try request(configuration: configuration, values: [
            "grant_type": "refresh_token", "refresh_token": refreshToken, "client_id": configuration.clientId
        ])
    }

    private static func request(configuration: NativeOAuthConfiguration, values: [String: String]) throws -> URLRequest {
        try configuration.validate()
        guard let url = URL(string: configuration.tokenEndpoint) else { throw OAuthError.invalidConfiguration }
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request.setValue("no-store", forHTTPHeaderField: "Cache-Control")
        var parameters = values
        if let resource = configuration.resource { parameters["resource"] = resource }
        request.httpBody = form(parameters)
        return request
    }

    public static func form(_ values: [String: String]) -> Data {
        let allowed = CharacterSet.alphanumerics.union(.init(charactersIn: "-._~"))
        let encoded = values.keys.sorted().map { key in
            "\(key.addingPercentEncoding(withAllowedCharacters: allowed)!)=\(values[key]!.addingPercentEncoding(withAllowedCharacters: allowed)!)"
        }.joined(separator: "&")
        return Data(encoded.utf8)
    }
}

public struct OAuthTokens: Decodable, Sendable {
    public let accessToken: String
    public let refreshToken: String?
    public let tokenType: String
    public let expiresIn: Int
    enum CodingKeys: String, CodingKey { case accessToken = "access_token", refreshToken = "refresh_token", tokenType = "token_type", expiresIn = "expires_in" }
    public func validate() throws {
        guard !accessToken.isEmpty, accessToken.utf8.count <= 16384,
              accessToken.rangeOfCharacter(from: .whitespacesAndNewlines.union(.controlCharacters)) == nil,
              tokenType.lowercased() == "bearer", expiresIn > 0 else { throw OAuthError.invalidTokens }
    }
}

private extension Data {
    var base64URLEncoded: String { base64EncodedString().replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "") }
}
