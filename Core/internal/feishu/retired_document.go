package feishu

// Only historical policy records retain these names; execution never accepts them.
func retiredDocumentPolicyID(id string) bool {
	switch id {
	case "docs.service.document.create", "docs.service.document.append", "docs.service.document.overwrite", "docs.whiteboard.insert", "docbox.version":
		return true
	}
	return false
}
