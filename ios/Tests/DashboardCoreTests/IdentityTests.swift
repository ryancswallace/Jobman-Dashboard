import Foundation
import Testing
@testable import DashboardCore

@Test func resourceIdentityIncludesSourceAndNamespace() {
    let a = JobRef(deploymentId: "a", namespaceId: "team", jobId: "same")
    let b = JobRef(deploymentId: "b", namespaceId: "team", jobId: "same")
    #expect(a != b)
    #expect(Set([a, b]).count == 2)
    #expect(DashboardRoute(url: DashboardRoute.job(a).url!) == .job(a))
}

@Test func rejectContentLinksWithInjectedAuthority() {
    for link in ["https://job/d/n/j", "jobman-dashboard://job/d/n/j?token=secret", "jobman-dashboard://user@job/d/n/j", "jobman-dashboard://job/d/n/..", "jobman-dashboard://job/d/n/j/extra", "jobman-dashboard://job/d/n/a%2Fb", "jobman-dashboard-auth://callback?code=c"] {
        #expect(DashboardRoute(url: URL(string: link)!) == nil, "Accepted \(link)")
    }
}

@Test func pathComponentsCannotEscapeResourceRoute() {
    let path = APIPath.job(.init(deploymentId: "a/b", namespaceId: "x?y", jobId: "j#z"))
    #expect(path == "/api/v1/deployments/a%2Fb/namespaces/x%3Fy/jobs/j%23z")
}

@Test func requirePrivateHTTPSOriginWithoutCredentials() throws {
    _ = try DashboardConnection(address: "https://dashboard.internal:8443")
    for address in ["http://dashboard.internal", "https://user:pass@dashboard.internal", "https://dashboard.internal/path", "https://dashboard.internal?token=secret", "file:///tmp/test"] {
        #expect(throws: DashboardError.self) { try DashboardConnection(address: address) }
    }
}

@Test func canonicalHTTPSContentLinksRequireConfiguredOriginAndExactRoute() throws {
    let connection = try DashboardConnection(address: "https://dashboard.internal:8443")
    let job = JobRef(deploymentId: "east", namespaceId: "research", jobId: "job-042")
    #expect(DashboardRoute(canonicalURL: URL(string: "https://dashboard.internal:8443/deployments/east/namespaces/research/jobs/job-042")!, connection: connection) == .job(job))
    #expect(DashboardRoute(canonicalURL: URL(string: "https://dashboard.internal:8443/inbox/opaque-id")!, connection: connection) == .inbox("opaque-id"))
    for address in [
        "https://other.internal:8443/inbox/id", "https://dashboard.internal/inbox/id", "http://dashboard.internal:8443/inbox/id",
        "https://user@dashboard.internal:8443/inbox/id", "https://dashboard.internal:8443/inbox/id?token=secret",
        "https://dashboard.internal:8443/inbox/id#secret", "https://dashboard.internal:8443/inbox/", "https://dashboard.internal:8443/inbox/a%2Fb",
        "https://dashboard.internal:8443/inbox/%69d", "https://dashboard.internal:8443/inbox/..", "https://dashboard.internal:8443/auth/callback",
        "https://dashboard.internal:8443/deployments/east/namespaces/research/jobs/job-042/logs"
    ] { #expect(DashboardRoute(canonicalURL: URL(string: address)!, connection: connection) == nil, "Accepted \(address)") }
}
