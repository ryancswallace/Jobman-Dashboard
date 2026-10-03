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
