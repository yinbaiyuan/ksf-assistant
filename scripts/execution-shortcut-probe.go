package main

import (
	"encoding/json"
	"os"
	"reflect"
	"runtime"
	"strings"

	"github.com/larksuite/cli/shortcuts"
)

func main() {
	rows := []map[string]any{}
	for _, shortcut := range shortcuts.AllShortcuts() {
		row := map[string]any{"path": shortcut.Service + " " + shortcut.Command, "description": shortcut.Description, "risk": shortcut.Risk, "flags": shortcut.Flags, "identities": shortcut.AuthTypes, "scopes": shortcut.Scopes, "userScopes": shortcut.UserScopes, "botScopes": shortcut.BotScopes, "conditionalScopes": shortcut.ConditionalScopes, "conditionalUserScopes": shortcut.ConditionalUserScopes, "conditionalBotScopes": shortcut.ConditionalBotScopes}
		for name, hook := range map[string]any{"execute": shortcut.Execute, "validate": shortcut.Validate, "normalize": shortcut.Normalize} {
			value := reflect.ValueOf(hook)
			if value.IsNil() {
				continue
			}
			function := runtime.FuncForPC(value.Pointer())
			file, line := function.FileLine(value.Pointer())
			if offset := strings.Index(file, "/shortcuts/"); offset >= 0 {
				file = file[offset+1:]
			}
			row[name] = map[string]any{"file": file, "line": line, "function": function.Name()}
		}
		rows = append(rows, row)
	}
	if err := json.NewEncoder(os.Stdout).Encode(rows); err != nil {
		panic(err)
	}
}
