import Darwin
import Foundation

public enum ProjectActionManifestError: Error, Equatable, LocalizedError {
    case invalidPermissions
    case unsupportedProtocol
    case invalidAction(String)
    case workingDirectoryOutsideRoot(String)

    public var errorDescription: String? {
        switch self {
        case .invalidPermissions:
            return "启动动作清单必须由当前用户拥有，且不可被组或其他用户写入。"
        case .unsupportedProtocol:
            return "启动动作清单协议版本不受支持。"
        case let .invalidAction(id):
            return "启动动作 \(id) 包含无效字段。"
        case let .workingDirectoryOutsideRoot(path):
            return "启动动作目录越出 Git 工程：\(path)"
        }
    }
}

public struct ProjectActionManifestLoader {
    public static let fileName = "codex-usage-bar.actions.json"
    private let fileManager: FileManager

    public init(fileManager: FileManager = .default) {
        self.fileManager = fileManager
    }

    public func load(mapping: KSFEngineeringMapping) throws -> [ProjectLaunchAction] {
        let root = URL(fileURLWithPath: mapping.rootPath, isDirectory: true)
            .resolvingSymlinksInPath()
            .standardizedFileURL
        let manifestURL = root.appendingPathComponent(Self.fileName)
        guard fileManager.fileExists(atPath: manifestURL.path) else { return [] }

        let attributes = try fileManager.attributesOfItem(atPath: manifestURL.path)
        let owner = (attributes[.ownerAccountID] as? NSNumber)?.uint32Value
        let permissions = (attributes[.posixPermissions] as? NSNumber)?.uint16Value ?? 0
        let type = attributes[.type] as? FileAttributeType
        guard type == .typeRegular, owner == getuid(), permissions & 0o022 == 0 else {
            throw ProjectActionManifestError.invalidPermissions
        }

        let manifest = try JSONDecoder().decode(RawManifest.self, from: Data(contentsOf: manifestURL))
        guard manifest.protocol == "codex-usage-bar-actions-v1" else {
            throw ProjectActionManifestError.unsupportedProtocol
        }

        var seen = Set<String>()
        return try manifest.actions.map { raw in
            guard
                !raw.id.isEmpty,
                seen.insert(raw.id).inserted,
                isSafe(raw.id),
                isSafe(raw.title),
                isSafe(raw.executable),
                raw.symbol.map(isSafe) ?? true,
                raw.arguments.allSatisfy(isSafe)
            else {
                throw ProjectActionManifestError.invalidAction(raw.id)
            }

            let relative = raw.workingDirectory ?? "."
            guard isSafe(relative), !(relative as NSString).isAbsolutePath else {
                throw ProjectActionManifestError.workingDirectoryOutsideRoot(relative)
            }
            let workingDirectory = (relative == "." ? root : root.appendingPathComponent(relative, isDirectory: true))
                .resolvingSymlinksInPath()
                .standardizedFileURL
            guard contains(workingDirectory, in: root) else {
                throw ProjectActionManifestError.workingDirectoryOutsideRoot(relative)
            }
            var isDirectory: ObjCBool = false
            guard fileManager.fileExists(atPath: workingDirectory.path, isDirectory: &isDirectory),
                  isDirectory.boolValue else {
                throw ProjectActionManifestError.invalidAction(raw.id)
            }

            return ProjectLaunchAction(
                id: raw.id,
                title: raw.title,
                symbol: raw.symbol ?? "play.fill",
                primary: raw.primary ?? false,
                executable: raw.executable,
                arguments: raw.arguments,
                workingDirectory: workingDirectory.path,
                engineeringID: mapping.id,
                engineeringRoot: root.path
            )
        }
    }

    private func contains(_ candidate: URL, in root: URL) -> Bool {
        candidate.path == root.path || candidate.path.hasPrefix(root.path + "/")
    }

    private func isSafe(_ value: String) -> Bool {
        !value.isEmpty && value.unicodeScalars.allSatisfy {
            !CharacterSet.controlCharacters.contains($0)
        }
    }

    private struct RawManifest: Decodable {
        let `protocol`: String
        let actions: [RawAction]
    }

    private struct RawAction: Decodable {
        let id: String
        let title: String
        let symbol: String?
        let primary: Bool?
        let executable: String
        let arguments: [String]
        let workingDirectory: String?

        init(from decoder: Decoder) throws {
            let container = try decoder.container(keyedBy: CodingKeys.self)
            id = try container.decode(String.self, forKey: .id)
            title = try container.decode(String.self, forKey: .title)
            symbol = try container.decodeIfPresent(String.self, forKey: .symbol)
            primary = try container.decodeIfPresent(Bool.self, forKey: .primary)
            executable = try container.decode(String.self, forKey: .executable)
            arguments = try container.decodeIfPresent([String].self, forKey: .arguments) ?? []
            workingDirectory = try container.decodeIfPresent(String.self, forKey: .workingDirectory)
        }

        private enum CodingKeys: String, CodingKey {
            case id, title, symbol, primary, executable, arguments, workingDirectory
        }
    }
}
