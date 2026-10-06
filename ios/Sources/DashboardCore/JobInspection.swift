import Foundation

/// Text presentation only: never joins arguments into an executable shell command.
public enum JobInspectionText {
    /// Make invisible controls inspectable while retaining normal multiline scripts.
    /// The original values remain untouched for lossless JSON copying.
    public static func literal(_ value: String) -> String {
        var result = ""
        for scalar in value.unicodeScalars {
            if scalar != "\n", scalar != "\t",
               [.control, .format, .lineSeparator, .paragraphSeparator].contains(scalar.properties.generalCategory) {
                result += "\\u{\(String(scalar.value, radix: 16, uppercase: true))}"
            } else { result.unicodeScalars.append(scalar) }
        }
        return result
    }

    /// argv[0] is the submitted executable; all remaining elements retain their
    /// submitted order, empty strings, whitespace and literal shell-wrapper text.
    public static func argumentVectorJSON(_ command: DashboardAPI.JobCommand) throws -> String {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .withoutEscapingSlashes]
        let data = try encoder.encode([command.executable] + command.args)
        guard let value = String(data: data, encoding: .utf8) else { throw DashboardError.invalidResponse }
        return value
    }
}
