import Foundation

private struct TestFailure: Error, CustomStringConvertible {
    let description: String
}

@main
private enum KSFBridgeStandaloneTestRunner {
    static func main() {
        let fileManager = FileManager.default
        let root = fileManager.temporaryDirectory.appendingPathComponent("ksf-bridge-\(UUID().uuidString)")
        let script = root.appendingPathComponent(
            ".agents/skills/ksf-load-route-context/scripts/ksf_panel_bridge.rb"
        )
        do {
            try fileManager.createDirectory(at: script.deletingLastPathComponent(), withIntermediateDirectories: true)
            try Data("# KSF test root\n".utf8).write(to: root.appendingPathComponent("AGENTS.md"))
            let source = #"""
            #!/usr/bin/ruby
            require 'json'
            if ARGV.include?('--enable')
              puts JSON.generate({'enabled' => true, 'enabledAt' => '2026-08-30T00:00:00.000000Z'})
            elsif ARGV.include?('--export-catalog')
              projects = 1500.times.map do |index|
                {'id' => "project-#{index}", 'name' => "Project #{index}", 'status' => 'active', 'summary' => 'x' * 80, 'memoryMode' => 'single', 'cardPath' => "/tmp/#{index}.md", 'projectDirectory' => "/tmp/#{index}", 'engineeringMappings' => []}
              end
              puts JSON.generate({'protocol' => 'ksf-panel-catalog-v1', 'generatedAt' => '2026-08-30T00:00:00Z', 'ksfRoot' => Dir.pwd, 'projects' => projects})
            elsif ARGV.include?('--resolve-projections')
              STDIN.read
              puts JSON.generate({'protocol' => 'ksf-task-project-resolution-v1', 'projections' => []})
            else
              warn 'unsupported'
              exit 2
            end
            """#
            try Data(source.utf8).write(to: script)
            try fileManager.setAttributes([.posixPermissions: 0o700], ofItemAtPath: script.path)

            let client = KSFBridgeClient()
            try client.validate(rootURL: root)
            let enabledAt = try client.enable(rootURL: root)
            guard enabledAt.timeIntervalSince1970 > 0 else {
                throw TestFailure(description: "enable response was not decoded")
            }
            let catalog = try client.fetchCatalog(rootURL: root)
            guard catalog.projects.count == 1500 else {
                throw TestFailure(description: "large catalog output was truncated or deadlocked")
            }
            let resolution = try client.resolve(rootURL: root, threadIDs: ["raw-thread-id"])
            guard resolution.projections.isEmpty else {
                throw TestFailure(description: "projection response was not decoded")
            }
            let invalidRoot = fileManager.temporaryDirectory.appendingPathComponent("invalid-ksf-\(UUID().uuidString)")
            try fileManager.createDirectory(at: invalidRoot, withIntermediateDirectories: true)
            defer { try? fileManager.removeItem(at: invalidRoot) }
            var rejectedInvalidRoot = false
            do {
                try client.validate(rootURL: invalidRoot)
            } catch {
                rejectedInvalidRoot = true
            }
            guard rejectedInvalidRoot else {
                throw TestFailure(description: "validation accepted a directory without KSF entrypoints")
            }
            print("PASS KSF bridge permissions and large concurrent output drain")
        } catch {
            fputs("FAIL \(error)\n", stderr)
            try? fileManager.removeItem(at: root)
            exit(1)
        }
        try? fileManager.removeItem(at: root)
    }
}
