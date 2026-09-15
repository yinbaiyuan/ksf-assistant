package feishucli

import "testing"

func TestCardProbeHasNoArbitraryEntityOrPayloadArguments(t *testing.T) {
	for _, args := range [][]string{
		{"card-probe", "start", "--id", "test-1", "--target-name", "self", "--mode", "native", "--as", "bot"},
		{"card-probe", "step", "--id", "test-1", "--as", "bot"},
	} {
		if _, err := Parse(args, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, flag := range []string{"card-id", "message-id", "data", "url", "sequence"} {
		if _, err := Parse([]string{"card-probe", "step", "--id", "test-1", "--as", "bot", "--" + flag, "arbitrary"}, nil); err == nil {
			t.Fatal(flag)
		}
	}
}
