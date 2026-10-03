import Foundation

public enum LogBufferError: Error, Equatable { case wrongStream, offsetGap, invalidEncoding, oversizedChunk }

/// Keeps original byte positions while bounding storage. Terminal escapes are never interpreted.
public struct LogBuffer: Sendable {
    public static let maximumBytes = 2 * 1024 * 1024
    public static let maximumChunkBytes = 256 * 1024
    public private(set) var streamIdentity: String?
    public private(set) var startOffset: UInt64 = 0
    public private(set) var nextOffset: UInt64 = 0
    public private(set) var evicted = false
    private var bytes = Data()
    public var byteCount: Int { bytes.count }

    public init() {}

    public mutating func append(base64: String, streamIdentity: String, offset: UInt64) throws {
        guard let data = Data(base64Encoded: base64) else { throw LogBufferError.invalidEncoding }
        guard data.count <= Self.maximumChunkBytes else { throw LogBufferError.oversizedChunk }
        if let current = self.streamIdentity {
            guard current == streamIdentity else { throw LogBufferError.wrongStream }
            guard offset == nextOffset else { throw LogBufferError.offsetGap }
        } else {
            self.streamIdentity = streamIdentity
            startOffset = offset
            nextOffset = offset
        }
        let (next, overflow) = offset.addingReportingOverflow(UInt64(data.count))
        guard !overflow else { throw LogBufferError.offsetGap }
        bytes.append(data)
        nextOffset = next
        if bytes.count > Self.maximumBytes {
            let removed = bytes.count - Self.maximumBytes
            bytes.removeFirst(removed)
            startOffset += UInt64(removed)
            evicted = true
        }
    }

    public var text: String {
        // Retaining bytes joins UTF-8 split across chunks; incomplete tails show replacement until completed.
        // Visible escape markers make ANSI/OSC content inert and explain altered presentation.
        let decoded = String(decoding: bytes, as: UTF8.self)
        var output = String()
        var lineLength = 0
        for scalar in decoded.unicodeScalars {
            if scalar == "\n" { output.unicodeScalars.append(scalar); lineLength = 0; continue }
            if lineLength == 16_384 { output += " ⟦long line continues⟧\n"; lineLength = 0 }
            switch scalar.value {
            case 9: output += "    "
            case 13: output += "␍"
            case 27: output += "␛"
            case 0...31, 127...159: output += "�"
            default: output.unicodeScalars.append(scalar)
            }
            lineLength += 1
        }
        return output
    }

    public mutating func clear() { self = LogBuffer() }
}
