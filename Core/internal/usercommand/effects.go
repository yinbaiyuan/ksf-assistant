package usercommand

import "strings"

func riskRank(risk string) int {
	switch risk {
	case "read":
		return 0
	case "write":
		return 1
	case "high-impact-write":
		return 2
	case "remote-operation":
		return 3
	case "destructive":
		return 4
	default:
		return 5
	}
}

func commandEffects(command Command, parsed parsed) []Effect {
	result := []Effect{}
	add := func(identifier, risk string) {
		for index, effect := range result {
			if effect.CapabilityID == identifier {
				if riskRank(risk) > riskRank(effect.Risk) {
					result[index].Risk = risk
				}
				return
			}
		}
		result = append(result, Effect{CapabilityID: identifier, Risk: risk})
	}
	for _, identifier := range strings.Fields(parsed.spec.capabilities) {
		add(identifier, parsed.spec.risk)
	}
	path := parsed.spec.path
	if path == "docs +update" && includes("overwrite str_replace", parsed.flags["command"]) && parsed.flags["command"] != "" {
		add("docs.shortcut.update", "destructive")
		add("docs.service.document.overwrite", "destructive")
	}
	if path == "im +messages-send" || path == "im +messages-reply" {
		for _, name := range strings.Fields(parsed.spec.files) {
			flag, _ := descriptorFlag(*parsed.spec.descriptor, name)
			for _, value := range parsed.values[name] {
				_, _, existing, _ := fileArgument(flag, value)
				if existing {
					continue
				}
				if name == "image" || name == "video-cover" {
					add("im.images.create", "write")
				} else {
					add("im.files.create", "write")
				}
			}
		}
	}
	if path == "drive +export" || path == "sheets +workbook-export" {
		add("drive.export_tasks.create", "write")
	}
	if path == "drive +upload" || path == "markdown +create" || path == "markdown +update" {
		add("drive.files.upload_all", "write")
		add("drive.metas.batch_query", "read")
	}
	if path == "docs +media-upload" || path == "docs +media-insert" || path == "slides +media-upload" || path == "okr +upload-image" {
		add("drive.medias.upload_all", "write")
	}
	if path == "task +upload-attachment" {
		add("task.attachments.upload", "write")
	}
	if parsed.flags["as"] == "bot" && contains([]string{"docs +create", "drive +upload", "markdown +create", "drive +import", "drive +create-folder", "drive +copy", "drive +task-get", "base +base-create", "base +base-copy", "slides +create", "wiki +node-create"}, path) {
		add("drive.permission.members.create", "high-impact-write")
	}
	if path == "markdown +overwrite" {
		add("drive.files.upload_all", "write")
	}
	if path == "base +record-upload-attachment" || path == "sheets +cells-set-image" || path == "sheets +float-image-create" || path == "sheets +float-image-update" || path == "docs +resource-update" {
		add("drive.medias.upload_all", "write")
	}
	if strings.HasPrefix(path, "mail ") && (len(parsed.values["attach"]) > 0 || parsed.flags["confirm-send"] == "true" || contains([]string{"mail +send", "mail +reply"}, path)) {
		add("mail.user_mailbox.drafts.create", "write")
		if parsed.flags["confirm-send"] == "true" {
			add("mail.user_mailbox.drafts.send", "high-impact-write")
		}
	}
	if path == "sheets +batch-update" {
		children, err := sheetBatchCommands(command, parsed)
		if err == nil {
			for _, child := range children {
				childParsed, _ := parse(child.Args)
				for _, effect := range commandEffects(child, childParsed) {
					add(effect.CapabilityID, effect.Risk)
				}
			}
		}
	}
	return result
}
