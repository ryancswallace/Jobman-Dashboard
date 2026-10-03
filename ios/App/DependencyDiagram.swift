import DashboardCore
import SwiftUI

struct DependencyDiagram: View {
    let children: [WorkloadChild]
    let totalNodes: String
    @State private var layout: GraphLayout?
    @State private var error: String?
    private let width = 160.0
    private let height = 75.0
    private var inputs: [GraphLayout.Input] { children.map { .init(id: $0.id, upstream: ($0.dependencies ?? []).map(\.upstreamNodeId)) } }
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Showing at most 200 nodes and 500 edges from this loaded page. Source total: \(totalNodes). Use node pages to reach all jobs.").font(.caption)
            if let layout {
                if layout.containsCycle { Label("Unexpected cycle in displayed dependencies", systemImage: "exclamationmark.triangle").font(.caption) }
                Text("\(layout.omittedNodes) loaded nodes and \(layout.omittedEdges) dependencies outside this diagram").font(.caption)
                ScrollView([.horizontal, .vertical]) {
                    ZStack(alignment: .topLeading) {
                        Canvas { context, _ in
                            let vertices = Dictionary(uniqueKeysWithValues: layout.vertices.map { ($0.id, $0) })
                            for edge in layout.edges {
                                guard let from = vertices[edge.from], let to = vertices[edge.to] else { continue }
                                let a = point(from), b = point(to)
                                var path = Path()
                                path.move(to: CGPoint(x: a.x + width / 2, y: a.y))
                                path.addLine(to: CGPoint(x: b.x - width / 2, y: b.y))
                                context.stroke(path, with: .color(.secondary), lineWidth: 1.5)
                            }
                        }.accessibilityHidden(true)
                        ForEach(layout.vertices) { vertex in
                            if let child = children.first(where: { $0.id == vertex.id }) {
                                NavigationLink { JobDetailView(ref: child.job.ref) } label: {
                                    VStack(alignment: .leading) {
                                        Text(child.id).font(.caption.bold()).lineLimit(2)
                                        Text(child.readiness ?? child.job.phase).font(.caption2)
                                    }.padding(8).frame(width: width, height: height, alignment: .leading)
                                        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 8))
                                        .overlay(RoundedRectangle(cornerRadius: 8).stroke(.secondary.opacity(0.5)))
                                }.buttonStyle(.plain).position(point(vertex))
                                    .accessibilityLabel("Node \(child.id), \(child.readiness ?? child.job.phase). Open job.")
                            }
                        }
                    }.frame(width: Double((layout.vertices.map(\.column).max() ?? 0) + 1) * (width + 48) + 16,
                            height: Double((layout.vertices.map(\.row).max() ?? 0) + 1) * (height + 24) + 16)
                }.frame(height: 320)
            } else if let error { ErrorMessage(error: error) }
            else { ProgressView("Laying out dependencies…") }
        }.task(id: inputs) {
            let snapshot = inputs
            let task = Task.detached(priority: .userInitiated) { try GraphLayout.calculate(snapshot) }
            do {
                let result = try await withTaskCancellationHandler { try await task.value } onCancel: { task.cancel() }
                try Task.checkCancellation()
                layout = result; error = nil
            } catch is CancellationError {} catch { self.error = error.localizedDescription }
        }
    }
    private func point(_ vertex: GraphLayout.Vertex) -> CGPoint {
        CGPoint(x: Double(vertex.column) * (width + 48) + width / 2 + 8, y: Double(vertex.row) * (height + 24) + height / 2 + 8)
    }
}
