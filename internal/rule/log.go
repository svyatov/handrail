package rule

import (
	"fmt"
	"os"
	"strings"
	"syscall"
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
		err = rotate(files)
		if err != nil {
			return err
		}
	}

	return appendFile(files.Current, lines...)
}

// rotate moves a full log to the older generation. Hooks that run at once all
// see it full, so the rotation holds a lock and checks again under it: the
// second hook would otherwise move the first one's fresh file over the full
// generation. The lock is a file beside the log, since the log itself is
// renamed away; appends take no lock.
func rotate(files LogFiles) error {
	lock, err := os.OpenFile(files.Current+".lock", os.O_CREATE|os.O_RDWR, registryMode)
	if err != nil {
		return err
	}

	defer func() { _ = lock.Close() }() // closing releases the lock

	err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX)
	if err != nil {
		return fmt.Errorf("locking %s: %w", lock.Name(), err)
	}

	// Another hook rotated it first.
	info, err := os.Stat(files.Current)
	if err != nil || info.Size() <= logRotateSize {
		return nil
	}

	return os.Rename(files.Current, files.Older)
}
