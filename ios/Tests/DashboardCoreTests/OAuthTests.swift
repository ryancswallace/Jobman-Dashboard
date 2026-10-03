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
    #expect(!body.contains("client_secret"))
}

@Test func noIdTokenCanSubstituteForAPICredential() {
    let invalid = Data(#"{"id_token":"do-not-use","token_type":"Bearer","expires_in":3600}"#.utf8)
    #expect(throws: DecodingError.self) { try JSONDecoder().decode(OAuthTokens.self, from: invalid) }
}
