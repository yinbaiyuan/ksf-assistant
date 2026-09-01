import Darwin
import Foundation

public enum ProjectStartScriptError: Error, Equatable, LocalizedError {
    case invalidProjectDirectory
    case invalidScript
    case invalidPermissions
    case notExecutable

    public var errorDescription: String? {
        switch self {
        case .invalidProjectDirectory:
            return "项目目录不可用。"
        case .invalidScript:
            return "start.sh 必须是项目目录内的普通文件，不能是符号链接。"
        case .invalidPermissions:
            return "start.sh 必须由当前用户拥有，且不可被组或其他用户写入。"
        case .notExecutable:
            return "start.sh 没有当前用户执行权限。"
        }
    }
}

public struct ProjectStartScriptResolver {
    public static let fileName = "start.sh"
    private let fileManager: FileManager

    public init(fileManager: FileManager = .default) {
        self.fileManager = fileManager
    }

    public func load(project: KSFProject) throws -> ProjectLaunchAction? {
        let root = try projectDirectory(project.projectDirectory)
        let script = root.appendingPathComponent(Self.fileName, isDirectory: false)
        guard fileManager.fileExists(atPath: script.path) else { return nil }
        try validateScript(script, in: root)
        return ProjectLaunchAction(
            projectID: project.id,
            scriptPath: script.path,
            workingDirectory: root.path
        )
    }

    public func validate(_ action: ProjectLaunchAction) throws {
        let root = try projectDirectory(action.workingDirectory)
        let script = URL(fileURLWithPath: action.scriptPath, isDirectory: false)
            .standardizedFileURL
        guard script.path == root.appendingPathComponent(Self.fileName).path else {
            throw ProjectStartScriptError.invalidScript
        }
        try validateScript(script, in: root)
    }

    private func projectDirectory(_ path: String) throws -> URL {
        guard isSafe(path), (path as NSString).isAbsolutePath else {
            throw ProjectStartScriptError.invalidProjectDirectory
        }
        let root = URL(fileURLWithPath: path, isDirectory: true)
            .resolvingSymlinksInPath()
            .standardizedFileURL
        var isDirectory: ObjCBool = false
        guard fileManager.fileExists(atPath: root.path, isDirectory: &isDirectory),
              isDirectory.boolValue else {
            throw ProjectStartScriptError.invalidProjectDirectory
        }
        return root
    }

    private func validateScript(_ script: URL, in root: URL) throws {
        guard script.deletingLastPathComponent().standardizedFileURL.path == root.path else {
            throw ProjectStartScriptError.invalidScript
        }
        let attributes = try fileManager.attributesOfItem(atPath: script.path)
        let owner = (attributes[.ownerAccountID] as? NSNumber)?.uint32Value
        let permissions = (attributes[.posixPermissions] as? NSNumber)?.uint16Value ?? 0
        let type = attributes[.type] as? FileAttributeType
        guard type == .typeRegular else { throw ProjectStartScriptError.invalidScript }
        guard owner == getuid(), permissions & 0o022 == 0 else {
            throw ProjectStartScriptError.invalidPermissions
        }
        guard permissions & 0o100 != 0 else {
            throw ProjectStartScriptError.notExecutable
        }
    }

    private func isSafe(_ value: String) -> Bool {
        !value.isEmpty && value.unicodeScalars.allSatisfy {
            !CharacterSet.controlCharacters.contains($0)
        }
    }
}
