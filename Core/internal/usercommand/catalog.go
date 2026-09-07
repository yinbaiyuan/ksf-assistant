package usercommand

import "strings"

type specification struct {
	descriptor   *ExecutionDescriptor
	path         string
	risk         string
	action       string
	capabilities string
	values       string
	switches     string
	required     string
	files        string
	targets      string
	body         bool
	yes          bool
}

var legacySpecifications = []specification{
	{path: "im +chat-list", risk: "read", action: "列出会话", capabilities: "im.shortcut.chat.list im.chat.list", values: "user-id-type sort types page-size page-token", switches: "exclude-muted", targets: "types"},
	{path: "im +chat-search", risk: "read", action: "搜索群聊", capabilities: "im.shortcut.chat.search im.chat.search", values: "query search-types chat-modes member-ids sort page-size page-token", switches: "is-manager disable-search-by-user exclude-muted", targets: "query member-ids"},
	{path: "im +chat-members-list", risk: "read", action: "查询群成员", capabilities: "im.chat.members.list", values: "chat-id member-types member-id-type page-size page-token page-limit page-delay", switches: "page-all", required: "chat-id", targets: "chat-id"},
	{path: "calendar calendars list", risk: "read", action: "查询日历列表", capabilities: "calendar.calendars.list", values: "page-size page-token sync-token"},
	{path: "calendar calendars get", risk: "read", action: "查询日历详情", capabilities: "calendar.calendars.get", values: "calendar-id", required: "calendar-id", targets: "calendar-id"},
	{path: "calendar calendars primary", risk: "read", action: "查询主日历", capabilities: "calendar.calendars.primary", values: "user-id-type"},
	{path: "drive files list", risk: "read", action: "查询文件夹内容", capabilities: "drive.files.list", values: "direction folder-token option order-by page-size page-token user-id-type", targets: "folder-token"},
	{path: "drive metas batch_query", risk: "read", action: "查询文件元数据", capabilities: "drive.metas.batch_query", values: "user-id-type data", required: "data", body: true},
	{path: "drive file.comments list", risk: "read", action: "查询文档评论", capabilities: "drive.file.comments.list", values: "file-token file-type page-size page-token user-id-type", switches: "is-solved is-whole need-reaction need-relation", required: "file-token file-type", targets: "file-token"},
	{path: "drive file.comment.replys list", risk: "read", action: "查询评论回复", capabilities: "drive.file.comment.replys.list", values: "comment-id file-token file-type page-size page-token user-id-type", switches: "need-reaction", required: "comment-id file-token file-type", targets: "file-token comment-id"},
	{path: "wiki spaces list", risk: "read", action: "查询知识空间列表", capabilities: "wiki.spaces.list", values: "page-size page-token"},
	{path: "wiki spaces get_node", risk: "read", action: "查询知识空间节点", capabilities: "wiki.spaces.get_node", values: "obj-type token", required: "token", targets: "token"},
	{path: "task tasks get", risk: "read", action: "查询任务详情", capabilities: "task.tasks.get", values: "task-guid user-id-type", required: "task-guid", targets: "task-guid"},
	{path: "task tasks list", risk: "read", action: "查询任务列表", capabilities: "task.tasks.list", values: "agent-task-status page-size page-token type user-id-type", switches: "completed"},
	{path: "im messages read_status", risk: "read", action: "查询消息已读状态（不标记已读）", capabilities: "im.messages.read_status im.message.read-status", values: "data", required: "data", body: true},
	{path: "im +messages-send", risk: "write", action: "发送飞书消息", capabilities: "im.shortcut.messages.send", values: "chat-id user-id msg-type text content idempotency-key image file", files: "image file", targets: "chat-id user-id", body: true},
	{path: "im +messages-reply", risk: "write", action: "回复飞书消息", capabilities: "im.shortcut.messages.reply im.message.reply", values: "message-id msg-type text content idempotency-key image file", switches: "reply-in-thread", required: "message-id", files: "image file", targets: "message-id", body: true},
	{path: "im +messages-edit", risk: "write", action: "更新已发送的消息", capabilities: "im.shortcut.messages.edit im.message.edit", values: "message-id text content msg-type", required: "message-id", targets: "message-id", body: true},
	{path: "im +messages-search", risk: "read", action: "搜索聊天消息", capabilities: "im.shortcut.messages.search", values: "query chat-id sender include-attachment-type chat-type sender-type exclude-sender-type at-chatter-ids start end page-size page-token page-limit", switches: "is-at-me no-reactions page-all", targets: "chat-id query"},
	{path: "im +chat-messages-list", risk: "read", action: "读取聊天消息", capabilities: "im.shortcut.chat.messages.list", values: "chat-id user-id start end order page-size page-token", switches: "no-reactions", targets: "chat-id user-id"},
	{path: "im +messages-mget", risk: "read", action: "读取指定消息", capabilities: "im.shortcut.messages.mget im.message.batch-get", values: "message-ids", targets: "message-ids"},
	{path: "contact +search-user", risk: "read", action: "查询通讯录", capabilities: "contact.user.search contact.user.get", values: "query user-ids queries lang page-size", switches: "has-chatted has-enterprise-email exclude-external-users left-organization", targets: "query user-ids queries"},
	{path: "docs +fetch", risk: "read", action: "读取文档", capabilities: "docs.shortcut.fetch", values: "doc doc-format detail lang revision-id scope start-block-id end-block-id keyword context-before context-after max-depth", required: "doc", targets: "doc"},
	{path: "docs +create", risk: "write", action: "创建文档（仅冻结的纯文本 Markdown）", capabilities: "docs.shortcut.create", values: "title content doc-format parent-token parent-position", required: "content doc-format", targets: "parent-token parent-position", body: true},
	{path: "docs +update", risk: "write", action: "修改文档（仅冻结的纯文本 Markdown）", capabilities: "docs.shortcut.update", values: "doc command content doc-format pattern revision-id", required: "doc command content doc-format", targets: "doc", body: true},
	{path: "calendar +agenda", risk: "read", action: "查看日历日程", capabilities: "calendar.shortcut.agenda", values: "calendar-id start end", targets: "calendar-id"},
	{path: "calendar +get", risk: "read", action: "读取日程", capabilities: "calendar.shortcut.get calendar.events.get", values: "calendar-id event-id", required: "event-id", targets: "calendar-id event-id"},
	{path: "im messages patch", risk: "write", action: "更新消息卡片", capabilities: "im.messages.patch im.message.edit", values: "message-id data", required: "message-id data", targets: "message-id", body: true},
	{path: "im messages delete", risk: "destructive", action: "撤回消息", capabilities: "im.messages.delete", values: "message-id", required: "message-id", targets: "message-id", yes: true},
	{path: "im images create", risk: "write", action: "上传图片", capabilities: "im.images.create", values: "data file", required: "data file", files: "file", targets: "file", body: true},
	{path: "im files create", risk: "write", action: "上传文件", capabilities: "im.files.create", values: "data file", required: "data file", files: "file", targets: "file", body: true},
	{path: "calendar events get", risk: "read", action: "读取日程", capabilities: "calendar.events.get calendar.shortcut.get", values: "calendar-id event-id user-id-type max-attendee-num", switches: "need-attendee need-meeting-settings", required: "calendar-id event-id", targets: "calendar-id event-id"},
	{path: "calendar events create", risk: "write", action: "创建日程", capabilities: "calendar.events.create calendar.shortcut.create calendar.create", values: "calendar-id user-id-type idempotency-key data", required: "calendar-id data", targets: "calendar-id", body: true},
	{path: "calendar events patch", risk: "write", action: "更新日程", capabilities: "calendar.events.patch calendar.shortcut.update calendar.update", values: "calendar-id event-id user-id-type data", required: "calendar-id event-id data", targets: "calendar-id event-id", body: true},
	{path: "calendar events delete", risk: "destructive", action: "删除日程（默认通知参会人）", capabilities: "calendar.events.delete", values: "calendar-id event-id need-notification", required: "calendar-id event-id", targets: "calendar-id event-id"},
	{path: "calendar events search_event", risk: "read", action: "搜索日程", capabilities: "calendar.events.search_event calendar.shortcut.search.event calendar.search", values: "calendar-id user-id-type page-size page-token data", required: "calendar-id data", targets: "calendar-id", body: true},
	{path: "calendar freebusys list", risk: "read", action: "查询忙闲", capabilities: "calendar.freebusys.list calendar.shortcut.freebusy", values: "user-id-type data", required: "data", body: true},
	{path: "drive files create_folder", risk: "write", action: "创建云盘文件夹", capabilities: "drive.files.create_folder drive.shortcut.create.folder", values: "data", required: "data", body: true},
}

type apiSpecification struct {
	method  string
	pattern string
	spec    specification
}

var apiSpecifications = []apiSpecification{
	{"POST", "/open-apis/task/v2/task_v2/task_subscription", specification{risk: "high-impact-write", action: "固定任务订阅", capabilities: "events.watch.task.add", values: "params data", body: true}},
	{"POST", "/open-apis/board/v1/whiteboards/:target/subscribe", specification{risk: "high-impact-write", action: "固定画板订阅", capabilities: "events.watch.whiteboard.add", values: "params data", body: true}},
	{"POST", "/open-apis/board/v1/whiteboards/:target/unsubscribe", specification{risk: "destructive", action: "取消固定画板订阅", capabilities: "events.watch.whiteboard.remove", values: "params data", body: true}},
	{"POST", "/open-apis/vc/v1/meetings/subscription", specification{risk: "high-impact-write", action: "固定会议订阅", capabilities: "events.watch.meeting.add", values: "params data", body: true}},
	{"POST", "/open-apis/vc/v1/meetings/unsubscription", specification{risk: "destructive", action: "取消固定会议订阅", capabilities: "events.watch.meeting.remove", values: "params data", body: true}},
	{"POST", "/open-apis/vc/v1/notes/subscription", specification{risk: "high-impact-write", action: "固定会议笔记订阅", capabilities: "events.watch.note.add", values: "params data", body: true}},
	{"POST", "/open-apis/vc/v1/notes/unsubscription", specification{risk: "destructive", action: "取消固定会议笔记订阅", capabilities: "events.watch.note.remove", values: "params data", body: true}},
	{"POST", "/open-apis/vc/v1/recordings/subscription", specification{risk: "high-impact-write", action: "固定录制订阅", capabilities: "events.watch.recording.add", values: "params data", body: true}},
	{"POST", "/open-apis/vc/v1/recordings/unsubscription", specification{risk: "destructive", action: "取消固定录制订阅", capabilities: "events.watch.recording.remove", values: "params data", body: true}},
	{"POST", "/open-apis/minutes/v1/minutes/subscription", specification{risk: "high-impact-write", action: "固定妙记订阅", capabilities: "events.watch.minutes.add", values: "params data", body: true}},
	{"POST", "/open-apis/minutes/v1/minutes/unsubscription", specification{risk: "destructive", action: "取消固定妙记订阅", capabilities: "events.watch.minutes.remove", values: "params data", body: true}},
	{"POST", "/open-apis/approval/v4/instances/subscription", specification{risk: "high-impact-write", action: "订阅审批实例事件（既有固定生命周期）", capabilities: "approval.events.instance.subscribe", values: "data", required: "data", body: true}},
	{"DELETE", "/open-apis/approval/v4/instances/subscription", specification{risk: "high-impact-write", action: "取消审批实例订阅", capabilities: "approval.events.instance.unsubscribe", values: "params", required: "params"}},
	{"POST", "/open-apis/approval/v4/tasks/subscription", specification{risk: "high-impact-write", action: "订阅审批任务事件（既有固定生命周期）", capabilities: "approval.events.task.subscribe", values: "data", required: "data", body: true}},
	{"DELETE", "/open-apis/approval/v4/tasks/subscription", specification{risk: "high-impact-write", action: "取消审批任务订阅", capabilities: "approval.events.task.unsubscribe", values: "params", required: "params"}},
	{"POST", "/open-apis/im/v1/messages", specification{risk: "write", action: "发送飞书消息", capabilities: "im.shortcut.messages.send", values: "params data", required: "params data", body: true}},
	{"POST", "/open-apis/im/v1/messages/:message-id/reply", specification{risk: "write", action: "回复飞书消息", capabilities: "im.shortcut.messages.reply im.message.reply", values: "data", required: "data", body: true}},
	{"GET", "/open-apis/im/v1/messages/:message-id", specification{risk: "read", action: "读取指定消息", capabilities: "im.shortcut.messages.mget im.message.batch-get", values: "params"}},
	{"PATCH", "/open-apis/im/v1/messages/:message-id", specification{risk: "write", action: "更新消息卡片", capabilities: "im.messages.patch im.message.edit", values: "data", required: "data", body: true}},
	{"DELETE", "/open-apis/im/v1/messages/:message-id", specification{risk: "destructive", action: "撤回消息", capabilities: "im.messages.delete"}},
	{"GET", "/open-apis/im/v1/messages", specification{risk: "read", action: "读取聊天消息", capabilities: "im.shortcut.chat.messages.list", values: "params", required: "params"}},
	{"POST", "/open-apis/im/v1/messages/search", specification{risk: "read", action: "搜索聊天消息（POST 查询）", capabilities: "im.shortcut.messages.search", values: "params data", required: "data", body: true}},
	{"POST", "/open-apis/im/v1/images", specification{risk: "write", action: "上传图片", capabilities: "im.images.create", values: "data file", required: "data file", files: "file", body: true}},
	{"POST", "/open-apis/im/v1/files", specification{risk: "write", action: "上传文件", capabilities: "im.files.create", values: "data file", required: "data file", files: "file", body: true}},
	{"POST", "/open-apis/drive/v1/files/:file-token/versions", specification{risk: "write", action: "创建文档版本", capabilities: "drive.file.version.create", values: "params data", required: "data", body: true}},
	{"GET", "/open-apis/calendar/v4/calendars/:calendar-id/events/:event-id", specification{risk: "read", action: "读取日程", capabilities: "calendar.events.get calendar.shortcut.get", values: "params"}},
	{"POST", "/open-apis/calendar/v4/calendars/:calendar-id/events", specification{risk: "write", action: "创建日程", capabilities: "calendar.events.create calendar.shortcut.create calendar.create", values: "params data", required: "data", body: true}},
	{"PATCH", "/open-apis/calendar/v4/calendars/:calendar-id/events/:event-id", specification{risk: "write", action: "更新日程", capabilities: "calendar.events.patch calendar.shortcut.update calendar.update", values: "params data", required: "data", body: true}},
	{"DELETE", "/open-apis/calendar/v4/calendars/:calendar-id/events/:event-id", specification{risk: "destructive", action: "删除日程（默认通知参会人）", capabilities: "calendar.events.delete", values: "params"}},
	{"POST", "/open-apis/calendar/v4/freebusy/list", specification{risk: "read", action: "查询忙闲（POST 查询）", capabilities: "calendar.freebusys.list calendar.shortcut.freebusy", values: "params data", required: "data", body: true}},
	{"POST", "/open-apis/drive/v1/files/create_folder", specification{risk: "write", action: "创建云盘文件夹", capabilities: "drive.files.create_folder drive.shortcut.create.folder", values: "data", required: "data", body: true}},
}

func includes(list, value string) bool {
	for _, entry := range strings.Fields(list) {
		if entry == value {
			return true
		}
	}
	return false
}

func matchPath(pattern, path string) bool {
	expected, actual := strings.Split(pattern, "/"), strings.Split(path, "/")
	if len(expected) != len(actual) {
		return false
	}
	for index, part := range expected {
		if strings.HasPrefix(part, ":") {
			if !safeIdentifier(actual[index]) {
				return false
			}
		} else if part != actual[index] {
			return false
		}
	}
	return true
}

func safeIdentifier(value string) bool {
	if value == "" || value == "." || value == ".." || len(value) > 512 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("_-@.", char)) {
			return false
		}
	}
	return true
}
