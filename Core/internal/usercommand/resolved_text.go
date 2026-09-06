package usercommand

import "strings"

func resolvedText(command Command, flag FlagDescriptor, value string) string {
	if includes("file media-file multipart", flag.Role) || len(flag.Input) == 0 {
		return value
	}
	if value == "-" && contains(flag.Input, "stdin") {
		return string(command.Stdin)
	}
	if strings.HasPrefix(value, "@@") {
		return value[1:]
	}
	if strings.HasPrefix(value, "@") && contains(flag.Input, "file") {
		for _, file := range command.Files {
			if file.Name == value[1:] {
				return string(file.Data)
			}
		}
	}
	return value
}
