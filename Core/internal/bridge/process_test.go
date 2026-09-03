package bridge

import (
	"strings"
	"testing"
)

func TestReplaceEnvironmentValueRemovesCaseInsensitiveDuplicates(t *testing.T) {
	result := replaceEnvironmentValue([]string{"PATH=/bin", "example=old", "EXAMPLE=older"}, "EXAMPLE", "new")
	matches := []string{}
	for _, entry := range result {
		if strings.HasPrefix(strings.ToUpper(entry), "EXAMPLE=") {
			matches = append(matches, entry)
		}
	}
	if len(matches) != 1 || matches[0] != "EXAMPLE=new" {
		t.Fatalf("unexpected environment: %#v", result)
	}
}
