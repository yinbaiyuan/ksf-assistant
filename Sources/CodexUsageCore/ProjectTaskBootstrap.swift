import Foundation

public enum ProjectTaskBootstrapError: Error, Equatable, LocalizedError {
    case invalidProjectName
    case invalidPath

    public var errorDescription: String? {
        switch self {
        case .invalidProjectName:
            return "项目名称无效，无法准备新任务。"
        case .invalidPath:
            return "项目路径无效，无法准备新任务。"
        }
    }
}

/// The inseparable input for creating a KSF-owned Codex task.
///
/// `cwd` is deliberately derived from the KSF root rather than an engineering
/// mapping. Codex Desktop uses that root to place the task in the saved KSF
/// project; engineering roots are context for the task, not its desktop owner.
public struct ProjectTaskBootstrap: Equatable, Sendable {
    public let cwd: String
    public let name: String
    public let prompt: String

    public init(cwd: String, name: String, prompt: String) {
        self.cwd = cwd
        self.name = name
        self.prompt = prompt
    }

    public static func prepare(
        project: KSFProject,
        ksfRootPath: String
    ) throws -> ProjectTaskBootstrap {
        guard isSafeText(project.name), !project.name.isEmpty else {
            throw ProjectTaskBootstrapError.invalidProjectName
        }
        let ksfRoot = try normalizedAbsolutePath(ksfRootPath)
        let projectCard = try normalizedAbsolutePath(project.cardPath)
        let name = explicitTaskName(projectName: project.name)

        let prompt = """
        这是 KSF 项目「\(project.name)」的新任务。
        项目记忆卡：\(projectCard)

        请按 KSF 规范加载该项目的基础上下文。本轮只做上下文准备：不要开始具体工作，不要修改文件，不要生成实施方案。完成后简短说明已就绪，并等待用户下一步指令。
        """

        return ProjectTaskBootstrap(cwd: ksfRoot, name: name, prompt: prompt)
    }

    private static func explicitTaskName(projectName: String) -> String {
        let suffix = " · 新任务"
        let available = max(1, 64 - suffix.count)
        return String(projectName.prefix(available)) + suffix
    }

    private static func normalizedAbsolutePath(_ value: String) throws -> String {
        guard value.hasPrefix("/"), isSafeText(value) else {
            throw ProjectTaskBootstrapError.invalidPath
        }
        return URL(fileURLWithPath: value).standardizedFileURL.path
    }

    private static func isSafeText(_ value: String) -> Bool {
        value.unicodeScalars.allSatisfy { !CharacterSet.controlCharacters.contains($0) }
    }
}
