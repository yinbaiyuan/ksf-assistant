package main

import (
	"context"
	"os"

	"ksfassistant/core/internal/taskruntime"
)

func main() {
	os.Exit(taskruntime.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, taskruntime.Options{}))
}
