import KSFAssistantCore
import Foundation
import ServiceManagement
import UserNotifications

enum NotificationPermissionState: String {
    case unknown = "尚未授权"
    case authorized = "已开启"
    case denied = "系统设置中已拒绝"
    case provisional = "暂时允许"
}

struct ResetNotificationController {
    private var center: UNUserNotificationCenter { .current() }

    func permissionState() async -> NotificationPermissionState {
        await withCheckedContinuation { continuation in
            center.getNotificationSettings { settings in
                switch settings.authorizationStatus {
                case .authorized, .ephemeral:
                    continuation.resume(returning: .authorized)
                case .provisional:
                    continuation.resume(returning: .provisional)
                case .denied:
                    continuation.resume(returning: .denied)
                case .notDetermined:
                    continuation.resume(returning: .unknown)
                @unknown default:
                    continuation.resume(returning: .unknown)
                }
            }
        }
    }

    func requestPermission() async -> NotificationPermissionState {
        do {
            _ = try await withCheckedThrowingContinuation {
                (continuation: CheckedContinuation<Bool, Error>) in
                center.requestAuthorization(options: [.alert, .sound]) { allowed, error in
                    if let error {
                        continuation.resume(throwing: error)
                    } else {
                        continuation.resume(returning: allowed)
                    }
                }
            }
        } catch {
            return .denied
        }
        return await permissionState()
    }

    func send(event: ResetEvent) {
        let content = UNMutableNotificationContent()
        content.title = "Codex 额度已重置"
        content.body = "通用 Codex 额度当前剩余 \(event.remainingPercent)% 。"
        content.sound = .default
        let request = UNNotificationRequest(
            identifier: "codex-reset-\(event.resetTimestamp)",
            content: content,
            trigger: nil
        )
        center.add(request)
    }
}

enum LoginItemState: String {
    case enabled = "已开启"
    case disabled = "已关闭"
    case requiresApproval = "等待系统设置批准"
    case unavailable = "请先安装到“应用程序”"
    case failed = "更新失败，请切换开关重试"
}

@MainActor
struct LoginItemController {
    func state() -> LoginItemState {
        guard Bundle.main.bundleURL.pathExtension == "app" else { return .unavailable }
        switch SMAppService.mainApp.status {
        case .enabled:
            return .enabled
        case .requiresApproval:
            return .requiresApproval
        case .notRegistered:
            return .disabled
        case .notFound:
            return .unavailable
        @unknown default:
            return .failed
        }
    }

    func setEnabled(_ enabled: Bool) -> LoginItemState {
        guard Bundle.main.bundleURL.pathExtension == "app" else { return .unavailable }
        do {
            if enabled {
                if SMAppService.mainApp.status == .notRegistered {
                    try SMAppService.mainApp.register()
                }
            } else if SMAppService.mainApp.status == .enabled || SMAppService.mainApp.status == .requiresApproval {
                try SMAppService.mainApp.unregister()
            }
            return state()
        } catch {
            return .failed
        }
    }
}
