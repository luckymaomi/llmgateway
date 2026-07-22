package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/luckymaomi/llmgateway/internal/releasearchive"
)

type entryFlags []string

func (entries *entryFlags) String() string {
	return fmt.Sprintf("%v", []string(*entries))
}

func (entries *entryFlags) Set(value string) error {
	*entries = append(*entries, value)
	return nil
}

func main() {
	var entries entryFlags
	sourceDirectory := flag.String("source", "", "source directory")
	outputPath := flag.String("output", "", "output ZIP path")
	modifiedValue := flag.String("modified", "", "canonical RFC3339 modification time")
	flag.Var(&entries, "entry", "archive entry path")
	flag.Parse()
	if flag.NArg() != 0 {
		exitWithError("release archive does not accept positional arguments")
	}
	modifiedAt, err := time.Parse(time.RFC3339, *modifiedValue)
	if err != nil {
		exitWithError("release archive modification time is invalid")
	}
	if err := releasearchive.Write(releasearchive.Options{
		SourceDirectory: *sourceDirectory,
		OutputPath:      *outputPath,
		ModifiedAt:      modifiedAt,
		Entries:         entries,
	}); err != nil {
		exitWithError(err.Error())
	}
}

func exitWithError(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
