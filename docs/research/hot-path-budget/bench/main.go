// Command bench measures the costs T36 asks about. It is a scratch module on a
// research branch, never built into handrail.
//
//	bench index <repo> <runs>     T21: git ls-files spawn vs in-process .git/index reads
//	bench log <dir> <runs>        T15: one Decision log append
//	bench parse <runs>            T32: 50 shell Examples through mvdan.cc/sh/v3/syntax
//	bench exec <runs> <cmd...>    a whole process, stdin from $BENCH_STDIN
package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

const target = ".handrail/local"

func main() {
	runs, _ := strconv.Atoi(os.Args[len(os.Args)-1])
	switch os.Args[1] {
	case "index":
		repo := os.Args[2]
		idx := filepath.Join(repo, ".git", "index")
		report("git ls-files spawn", runs, func() bool { return gitTracked(repo) })
		report("in-process, full read", runs, func() bool { return must(indexTracked(idx, false)) })
		report("in-process, early exit", runs, func() bool { return must(indexTracked(idx, true)) })
	case "log":
		dir := os.Args[2]
		line := logLine()
		report(fmt.Sprintf("append %d B, no fsync", len(line)), runs, func() bool { return appendLog(dir, line, false) })
		report(fmt.Sprintf("append %d B, fsync", len(line)), runs, func() bool { return appendLog(dir, line, true) })
	case "parse":
		report(fmt.Sprintf("parse %d commands", len(commands)), runs, parseAll)
	case "exec":
		argv := os.Args[3 : len(os.Args)-1]
		runs, _ = strconv.Atoi(os.Args[2])
		stdin := os.Getenv("BENCH_STDIN")
		report(strings.Join(argv, " "), runs, func() bool {
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Stdin = strings.NewReader(stdin)
			return cmd.Run() == nil
		})
	}
}

// report runs f once to warm the page cache, then runs times, and prints the
// median and p90 of the wall time.
func report(name string, runs int, f func() bool) {
	result := f()
	times := make([]time.Duration, runs)
	for i := range times {
		start := time.Now()
		f()
		times[i] = time.Since(start)
	}
	slices.Sort(times)
	fmt.Printf("%-32s median %9v  p90 %9v  (result %v, %d runs)\n",
		name, times[runs/2].Round(time.Microsecond/10), times[runs*9/10].Round(time.Microsecond/10), result, runs)
}

func must(b bool, err error) bool {
	if err != nil {
		panic(err)
	}
	return b
}

// gitTracked is option 1: ask git whether any path under the target is in
// the index. Exit 0 means tracked, exit 1 means not.
func gitTracked(repo string) bool {
	cmd := exec.Command("git", "-C", repo, "ls-files", "--error-unmatch", "--", target)
	return cmd.Run() == nil
}

// indexTracked is option 2: read .git/index in process. Entries are sorted by
// path, so early can stop at the first path past the target. It handles index
// versions 2, 3 and 4 and a sparse directory entry, and assumes SHA-1 names.
func indexTracked(path string, early bool) (bool, error) {
	var r *bufio.Reader
	if early {
		f, err := os.Open(path)
		if err != nil {
			return false, err
		}
		defer f.Close()
		r = bufio.NewReaderSize(f, 16<<10)
	} else {
		data, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		r = bufio.NewReader(bytes.NewReader(data))
	}
	var hdr [12]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return false, err
	}
	if string(hdr[:4]) != "DIRC" {
		return false, errors.New("not an index")
	}
	version := binary.BigEndian.Uint32(hdr[4:8])
	count := binary.BigEndian.Uint32(hdr[8:12])
	const fixed = 62 // stat data 40, object name 20, flags 2
	var entry [fixed + 2]byte
	var prev []byte
	found := false
	for range count {
		if _, err := io.ReadFull(r, entry[:fixed]); err != nil {
			return false, err
		}
		flags := binary.BigEndian.Uint16(entry[60:62])
		size := fixed
		if version >= 3 && flags&0x4000 != 0 {
			if _, err := io.ReadFull(r, entry[fixed:fixed+2]); err != nil {
				return false, err
			}
			size += 2
		}
		var name []byte
		if version == 4 {
			strip, err := varint(r)
			if err != nil {
				return false, err
			}
			suffix, err := r.ReadBytes(0)
			if err != nil {
				return false, err
			}
			name = append(prev[:len(prev)-int(strip)], suffix[:len(suffix)-1]...)
			prev = name
		} else {
			n, err := r.ReadBytes(0)
			if err != nil {
				return false, err
			}
			name = n[:len(n)-1]
			// Pad to a multiple of 8 with one to eight NULs; ReadBytes took one.
			pad := 8 - (size+len(name))%8
			if _, err := r.Discard(pad - 1); err != nil {
				return false, err
			}
		}
		p := string(name)
		switch {
		case p == target, strings.HasPrefix(p, target+"/"):
			found = true
		case strings.HasSuffix(p, "/") && strings.HasPrefix(target+"/", p):
			found = true // a sparse directory entry holding the target
		case early && p > target+"/":
			return false, nil
		}
		if found && early {
			return true, nil
		}
	}
	return found, nil
}

// varint is git's offset encoding, which index v4 uses for the strip length.
func varint(r *bufio.Reader) (uint64, error) {
	c, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	v := uint64(c & 0x7f)
	for c&0x80 != 0 {
		if c, err = r.ReadByte(); err != nil {
			return 0, err
		}
		v = ((v + 1) << 7) | uint64(c&0x7f)
	}
	return v, nil
}

// logLine is a realistic Decision log line: a Bash call matching one rule.
func logLine() []byte {
	cmd := "cd /Users/someone/Projects/app && rm -rf node_modules dist .cache && npm ci && npm run build -- --prod"
	return fmt.Appendf(nil,
		`{"time":"2026-09-23T10:11:12.345Z","root":"/Users/someone/Projects/app","harness":"claude","event":"PreToolUse","kind":"shell","tool":"Bash","rules":[{"name":"no-rm-rf","tier":"global","action":"block"}],"outcome":"block","payload":{"command":%q,"cwd":"/Users/someone/Projects/app"}}`+"\n", cmd)
}

// appendLog is the hot-path write T15 decided: read the grant, create the
// state dir if needed, one O_APPEND write, no lock.
func appendLog(dir string, line []byte, sync bool) bool {
	grants, err := os.ReadFile(filepath.Join(dir, "logged"))
	if err != nil || !bytes.Contains(grants, []byte("/Users/someone/Projects/app\n")) {
		return false
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false
	}
	f, err := os.OpenFile(filepath.Join(dir, "log.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	_, err = f.Write(line)
	if sync {
		err = errors.Join(err, f.Sync())
	}
	return errors.Join(err, f.Close()) == nil
}

// parseAll is T2 plus T31 for one Example set: parse each command, collect
// every call, and re-parse the literal code a shell is handed through -c, up
// to four levels.
func parseAll() bool {
	calls := 0
	for _, c := range commands {
		n, err := candidates(c, 0)
		if err != nil {
			return false
		}
		calls += n
	}
	return calls > len(commands)
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "mksh": true}

func candidates(src string, depth int) (int, error) {
	f, err := syntax.NewParser().Parse(strings.NewReader(src), "")
	if err != nil {
		return 0, err
	}
	calls := 0
	var nested []string
	syntax.Walk(f, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		calls++
		if len(call.Args) > 2 && shells[call.Args[0].Lit()] {
			for i, a := range call.Args[1 : len(call.Args)-1] {
				if l := a.Lit(); strings.HasPrefix(l, "-") && strings.Contains(l, "c") {
					if code, ok := literal(call.Args[i+2]); ok {
						nested = append(nested, code)
					}
				}
			}
		}
		return true
	})
	if depth < 4 {
		for _, code := range nested {
			n, err := candidates(code, depth+1)
			if err != nil {
				return 0, err
			}
			calls += n
		}
	}
	return calls, nil
}

// literal returns a word's value when it is fixed text: unquoted, single- or
// double-quoted with no expansion.
func literal(w *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, q := range p.Parts {
				l, ok := q.(*syntax.Lit)
				if !ok {
					return "", false
				}
				b.WriteString(l.Value)
			}
		default:
			return "", false
		}
	}
	return b.String(), true
}
