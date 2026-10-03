import Foundation
import Testing
@testable import DashboardCore

@Test func splitUTF8AndTerminalControlsPreserveOffsets() throws {
    var buffer = LogBuffer()
    try buffer.append(base64: Data([0xf0, 0x9f]).base64EncodedString(), streamIdentity: "run1/stdout", offset: 4)
    try buffer.append(base64: Data([0x92, 0xa1, 0x1b, 0x00]).base64EncodedString(), streamIdentity: "run1/stdout", offset: 6)
    #expect(buffer.text == "💡␛�")
    #expect(buffer.startOffset == 4)
    #expect(buffer.nextOffset == 10)
    #expect(throws: LogBufferError.wrongStream) { try buffer.append(base64: "", streamIdentity: "run2/stdout", offset: 10) }
    #expect(throws: LogBufferError.offsetGap) { try buffer.append(base64: "", streamIdentity: "run1/stdout", offset: 11) }
}

@Test func memoryIsBoundedAndEvictionExplicit() throws {
    var buffer = LogBuffer()
    let chunk = Data(repeating: 65, count: LogBuffer.maximumChunkBytes).base64EncodedString()
    for index in 0..<9 { try buffer.append(base64: chunk, streamIdentity: "run/stdout", offset: UInt64(index * LogBuffer.maximumChunkBytes)) }
    #expect(buffer.byteCount == LogBuffer.maximumBytes)
    #expect(buffer.evicted)
    #expect(buffer.startOffset == UInt64(LogBuffer.maximumChunkBytes))
    #expect(buffer.nextOffset == UInt64(9 * LogBuffer.maximumChunkBytes))
}

private func logChunk(_ data: Data, offset: UInt64, run: String = "run1", execution: String = "execution1", cursor: String? = "next") throws -> LogChunk {
    var json: [String: Any] = ["bytesBase64":data.base64EncodedString(), "stream":"stdout", "runId":run, "executionId":execution,
                              "startOffset":String(offset), "endOffset":String(offset + UInt64(data.count)), "state":"active", "truncated":true]
    json["nextCursor"] = cursor
    return try JSONDecoder().decode(LogChunk.self, from: JSONSerialization.data(withJSONObject: json))
}

@Test(arguments: ["cursor_expired", "stream_changed", "log_gap"])
func logRefreshResetsCursorDecoderExecutionAndEndAfterDiscontinuity(code: String) throws {
    let job = JobRef(deploymentId: "east", namespaceId: "team", jobId: "job1")
    var read = LogReadState()
    try read.accept(logChunk(Data([0xf0, 0x9f]), offset: 100), job: job, stream: "stdout")
    read.failed(DashboardError.from(code: code, status: 409))
    #expect(read.requiresRefresh)
    #expect(read.cursor == "next")
    #expect(read.buffer.nextOffset == 102)
    #expect(throws: DashboardError.invalidResponse) { try read.accept(logChunk(Data(), offset: 102), job: job, stream: "stdout") }
    read.reset()
    #expect(!read.requiresRefresh)
    #expect(read.cursor == nil)
    #expect(read.buffer.streamIdentity == nil)
    #expect(read.buffer.byteCount == 0)
    #expect(read.buffer.nextOffset == 0)
    #expect(!read.truncated)
    #expect(read.state.isEmpty)
    try read.accept(logChunk(Data("new execution".utf8), offset: 0, run: "run2", execution: "execution2", cursor: nil), job: job, stream: "stdout")
    #expect(read.buffer.text == "new execution")
    #expect(read.buffer.streamIdentity?.contains("execution2") == true)
}

@Test func logChunkValidationCannotPartiallyAdvanceCursorOrBytes() throws {
    let job = JobRef(deploymentId: "east", namespaceId: "team", jobId: "job1")
    var read = LogReadState()
    try read.accept(logChunk(Data("original".utf8), offset: 0), job: job, stream: "stdout")
    #expect(throws: LogBufferError.wrongStream) { try read.accept(logChunk(Data("new".utf8), offset: 8, execution: "other", cursor: "other-cursor"), job: job, stream: "stdout") }
    #expect(read.buffer.text == "original")
    #expect(read.cursor == "next")
    #expect(read.buffer.nextOffset == 8)
    read.failed(LogBufferError.wrongStream)
    #expect(read.requiresRefresh)
    var networkFailure = LogReadState()
    networkFailure.failed(DashboardError.network)
    #expect(!networkFailure.requiresRefresh)
}
