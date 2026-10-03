import Foundation
import Testing
@testable import DashboardCore

private func config() -> NativeOAuthConfiguration {
    .init(issuer: "https://adfs.example.test/adfs", authorizationEndpoint: "https://adfs.example.test/adfs/oauth2/authorize",
          tokenEndpoint: "https://adfs.example.test/adfs/oauth2/token", clientId: "native-fixture", redirectURI: "jobman-dashboard-auth://callback",
          scopes: ["openid", "offline_access"], resource: "https://dashboard.example.test")
}

@Test func pkceMatchesRFC7636Vector() {
    #expect(OAuthAttempt.challenge(verifier: "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk") == "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM")
}

@Test func callbackMustMatchAttemptAndContainExactlyOneCodeAndState() throws {
    let attempt = try OAuthAttempt(configuration: config())
    #expect(try attempt.authorizationCode(callback: URL(string: "jobman-dashboard-auth://callback?code=fixture&state=\(attempt.state)")!) == "fixture")
    for callback in ["jobman-dashboard-auth://callback?code=fixture&state=wrong", "jobman-dashboard-auth://callback?code=a&code=b&state=\(attempt.state)", "jobman-dashboard-auth://callback?code=a&state=\(attempt.state)&state=\(attempt.state)", "jobman-dashboard-auth://evil?code=a&state=\(attempt.state)"] {
        #expect(throws: OAuthError.self) { try attempt.authorizationCode(callback: URL(string: callback)!) }
    }
    let request = try attempt.tokenRequest(code: "a+b&other=value")
    let body = String(data: request.httpBody!, encoding: .utf8)!
    #expect(body.contains("code=a%2Bb%26other%3Dvalue"))
    #expect(body.contains("resource=https%3A%2F%2Fdashboard.example.test"))
    #expect(!body.contains("client_secret"))
}

@Test func discoveryCannotSendCodeOrRefreshCredentialOutsideIssuerOrigin() throws {
    for endpoint in ["https://other.example.test/token", "http://adfs.example.test/token", "https://adfs.example.test:8443/token", "https://adfs.example.test/token?audience=other"] {
        let bad = NativeOAuthConfiguration(issuer: config().issuer, authorizationEndpoint: config().authorizationEndpoint,
            tokenEndpoint: endpoint, clientId: config().clientId, redirectURI: config().redirectURI, scopes: config().scopes, resource: config().resource)
        #expect(throws: OAuthError.invalidConfiguration) { try OAuthAttempt(configuration: bad) }
        #expect(throws: OAuthError.invalidConfiguration) { try OAuthAttempt.refreshRequest(configuration: bad, refreshToken: "synthetic-secret") }
    }
    let request = try OAuthAttempt.refreshRequest(configuration: config(), refreshToken: "synthetic+a&b")
    let body = String(data: request.httpBody!, encoding: .utf8)!
    #expect(request.url?.absoluteString == config().tokenEndpoint)
    #expect(body.contains("resource=https%3A%2F%2Fdashboard.example.test"))
    #expect(body.contains("refresh_token=synthetic%2Ba%26b"))
    #expect(!body.contains("client_secret"))
}

@Test func malformedBearerCredentialCannotReachHTTPHeaders() throws {
    let data = Data(#"{"access_token":"synthetic\r\nOther: value","token_type":"Bearer","expires_in":3600}"#.utf8)
    let tokens = try JSONDecoder().decode(OAuthTokens.self, from: data)
    #expect(throws: OAuthError.invalidTokens) { try tokens.validate() }
}

@Test func noIdTokenCanSubstituteForAPICredential() {
    let invalid = Data(#"{"id_token":"do-not-use","token_type":"Bearer","expires_in":3600}"#.utf8)
    #expect(throws: DecodingError.self) { try JSONDecoder().decode(OAuthTokens.self, from: invalid) }
}
