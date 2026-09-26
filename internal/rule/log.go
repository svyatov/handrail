package rule

import (
	"os"
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

// LogFiles are the Decision log's two generations, both "" when there is no
// state dir.
type LogFiles struct {
	Older, Current string
}

// LogPaths are the Decision log's files.
func LogPaths() LogFiles {
	file := registryFile(logFile)
	if file == "" {
		return LogFiles{Older: "", Current: ""}
	}

	return LogFiles{Older: file + ".1", Current: file}
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

// AppendLog appends lines to the Decision log, each one write, and nothing with
// no state dir, where there is no log. Past logRotateSize the file first
// becomes the one older generation.
func AppendLog(lines [][]byte) error {
	files := LogPaths()
	if files.Current == "" {
		return nil
	}

	info, err := os.Stat(files.Current)
	if err == nil && info.Size() > logRotateSize {
		err = os.Rename(files.Current, files.Older)
		if err != nil {
			return err
		}
	}

	return appendFile(files.Current, lines...)
}
