import Foundation

private func runUUID(_ text: String?) -> Bool {
    guard let text, let value = UUID(uuidString: text), value.uuidString.lowercased() == text else { return false }
    return text != "00000000-0000-0000-0000-000000000000"
}
private func runDecimal(_ text: String, positive: Bool = false) -> Int64? {
    guard let value = Int64(text), value >= (positive ? 1 : 0), String(value) == text else { return nil }
    return value
}
extension DashboardAPI.JobRun {
    public func validate() throws {
        guard runUUID(id), runDecimal(number, positive: true) != nil,
              WireDate.parse(createdAt) != nil, WireDate.parse(updatedAt) != nil else { throw DashboardError.invalidResponse }
        for text in [Optional(phase), Optional(desiredState), outcome, executionPhase, backend, confidence] {
            if let text, text.isEmpty || text.utf8.count > 64 { throw DashboardError.invalidResponse }
        }
        if executionId != nil {
            guard runUUID(executionId), runUUID(targetId), runUUID(targetGenerationId),
                  executionPhase != nil, backend != nil else { throw DashboardError.invalidResponse }
        } else {
            guard executionPhase == nil, targetId == nil, targetGenerationId == nil,
                  backend == nil, confidence == nil else { throw DashboardError.invalidResponse }
        }
    }
    public var logQuery: [URLQueryItem] { [.init(name: "runNumber", value: number)] }
    public func validate(log: LogChunk) throws {
        try validate()
        guard log.runId == id, log.runNumber == number,
              log.executionId == (executionId ?? "") else { throw DashboardError.invalidResponse }
    }
    public func validate(artifacts: [Artifact]) throws {
        try validate()
        guard executionId != nil || artifacts.isEmpty,
              artifacts.allSatisfy({ $0.runId == id && $0.runNumber == number && $0.executionId == executionId }) else { throw DashboardError.invalidResponse }
    }
}
extension DashboardAPI.RunPage {
    public func validate(job: JobRef) throws {
        guard completeness == "complete", items.count <= 100, Set(items.map(\.id)).count == items.count,
              Set(items.map(\.number)).count == items.count,
              let count = runDecimal(total), count >= items.count,
              WireDate.parse(fetchedAt) != nil, sources.count == 1,
              sources[0].deploymentId == job.deploymentId, sources[0].namespaceId == job.namespaceId,
              sources[0].status == "available", sources[0].asOf.flatMap(WireDate.parse) != nil,
              WireDate.parse(sources[0].fetchedAt) != nil else { throw DashboardError.invalidResponse }
        if let nextCursor {
            guard !nextCursor.isEmpty, nextCursor.utf8.count <= 512, !items.isEmpty, count > items.count else { throw DashboardError.invalidResponse }
        }
        try items.forEach { try $0.validate() }
        let numbers = items.compactMap { Int64($0.number) }
        guard numbers == numbers.sorted(by: >) else { throw DashboardError.invalidResponse }
    }
}
