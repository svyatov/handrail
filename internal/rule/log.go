package rule

import (
	"os"
	"path/filepath"
	"strings"
)

// The Decision log's grant and its file. The grant sits in the state registry
// beside Trust: each line is on or off, a space, and the Project root, and the
// last line for a root holds, so revoking one appends like granting it.

const (
	logGrantFile = "decision-log"
	logFile      = "log.jsonl"
	// logRotateSize is where the log rotates to its one older generation.
	logRotateSize = 5 << 20
)

// LogPaths are the Decision log's files, the older generation first, and nil
// when there is no state dir.
func LogPaths() []string {
	file := registryFile(logFile)
	if file == "" {
		return nil
	}

	return []string{file + ".1", file}
}

// Logging reports whether root holds a Decision log grant. A registry that
// cannot be read grants nothing.
func Logging(root string) bool {
	granted := false
	lines, _ := registry(logGrantFile)

	for _, line := range lines {
		if on, path, _ := strings.Cut(line, " "); path == root {
			granted = on == "on"
		}
	}

	return granted
}

// SetLogging grants or revokes root's Decision log grant. Revoking deletes no
// line of the log.
func SetLogging(root string, on bool) error {
	line := "off " + root
	if on {
		line = "on " + root
	}

	return appendRegistry(logGrantFile, line)
}

// AppendLog appends lines to the Decision log, each one write to a file opened
// for appending, and nothing with no state dir, where there is no log. Past
// logRotateSize the file first becomes the one older generation.
func AppendLog(lines [][]byte) error {
	paths := LogPaths()
	if paths == nil {
		return nil
	}

	older, file := paths[0], paths[1]

	err := os.MkdirAll(filepath.Dir(file), registryDirMode)
	if err != nil {
		return err
	}

	info, statErr := os.Stat(file)
	if statErr == nil && info.Size() > logRotateSize {
		err = os.Rename(file, older)
		if err != nil {
			return err
		}
	}

	out, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, registryMode)
	if err != nil {
		return err
	}

	for _, line := range lines {
		_, err = out.Write(line)
		if err != nil {
			break
		}
	}

	if closeErr := out.Close(); err == nil {
		err = closeErr
	}

	return err
}
