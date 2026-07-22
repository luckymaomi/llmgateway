package releasearchive

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type Options struct {
	SourceDirectory string
	OutputPath      string
	ModifiedAt      time.Time
	Entries         []string
}

func Write(options Options) (returnErr error) {
	if len(options.Entries) == 0 {
		return errors.New("release archive requires at least one entry")
	}
	if options.ModifiedAt.IsZero() {
		return errors.New("release archive requires a modification time")
	}

	sourceDirectory, err := filepath.Abs(options.SourceDirectory)
	if err != nil {
		return fmt.Errorf("resolve release archive source: %w", err)
	}
	sourceInfo, err := os.Stat(sourceDirectory)
	if err != nil {
		return fmt.Errorf("inspect release archive source: %w", err)
	}
	if !sourceInfo.IsDir() {
		return errors.New("release archive source must be a directory")
	}

	outputPath, err := filepath.Abs(options.OutputPath)
	if err != nil {
		return fmt.Errorf("resolve release archive output: %w", err)
	}
	entries := slices.Clone(options.Entries)
	slices.Sort(entries)
	for index, entry := range entries {
		if err := validateEntry(entry); err != nil {
			return err
		}
		if index > 0 && entries[index-1] == entry {
			return fmt.Errorf("release archive entry is duplicated: %s", entry)
		}
	}

	output, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create release archive: %w", err)
	}
	defer func() {
		if closeErr := output.Close(); closeErr != nil && returnErr == nil {
			returnErr = fmt.Errorf("close release archive: %w", closeErr)
		}
		if returnErr != nil {
			_ = os.Remove(outputPath)
		}
	}()

	archive := zip.NewWriter(output)
	for _, entry := range entries {
		if err := writeEntry(archive, sourceDirectory, entry, options.ModifiedAt.UTC()); err != nil {
			_ = archive.Close()
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return fmt.Errorf("finish release archive: %w", err)
	}
	return nil
}

func validateEntry(entry string) error {
	if !fs.ValidPath(entry) || path.IsAbs(entry) || strings.Contains(entry, `\`) || strings.HasSuffix(entry, "/") {
		return fmt.Errorf("release archive entry is not a canonical relative path: %q", entry)
	}
	for _, component := range strings.Split(entry, "/") {
		for _, character := range component {
			if (character >= 'a' && character <= 'z') ||
				(character >= 'A' && character <= 'Z') ||
				(character >= '0' && character <= '9') ||
				character == '.' || character == '_' || character == '-' {
				continue
			}
			return fmt.Errorf("release archive entry contains an unsupported character: %q", entry)
		}
	}
	return nil
}

func writeEntry(archive *zip.Writer, sourceDirectory, entry string, modifiedAt time.Time) error {
	sourcePath := filepath.Join(sourceDirectory, filepath.FromSlash(entry))
	relativePath, err := filepath.Rel(sourceDirectory, sourcePath)
	if err != nil || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return fmt.Errorf("release archive entry escaped its source: %s", entry)
	}
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return fmt.Errorf("inspect release archive entry %s: %w", entry, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("release archive entry must be a regular file: %s", entry)
	}

	header := &zip.FileHeader{Name: entry, Method: zip.Deflate, Modified: modifiedAt}
	header.SetMode(releaseMode(entry))
	destination, err := archive.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("create release archive entry %s: %w", entry, err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open release archive entry %s: %w", entry, err)
	}
	_, copyErr := io.Copy(destination, source)
	closeErr := source.Close()
	if copyErr != nil {
		return fmt.Errorf("write release archive entry %s: %w", entry, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close release archive entry %s: %w", entry, closeErr)
	}
	return nil
}

func releaseMode(entry string) fs.FileMode {
	if strings.HasSuffix(entry, ".sh") || !strings.Contains(entry, "/") && strings.HasPrefix(entry, "llmgateway") {
		return 0o755
	}
	return 0o644
}
