public enum LocalTokenRefreshPolicy {
    public static let activeIntervalNanoseconds: UInt64 = 10_000_000_000

    public static func shouldPoll(for activity: TaskActivitySnapshot) -> Bool {
        activity.availability == .available && activity.runningCount > 0
    }
}
