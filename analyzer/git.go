package analyzer

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// gitEnv makes git fail fast instead of prompting for credentials (private or
// nonexistent repos on GitHub answer with an auth challenge).
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"LC_ALL=C",
	)
}

func gitCmd(ctx context.Context, dir string, args ...string) *exec.Cmd {
	base := []string{"-c", "credential.helper=", "-c", "core.quotePath=false"}
	if dir != "" {
		base = append(base, "-C", dir)
	}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Env = gitEnv()
	return cmd
}

// runGit runs a short git command and returns its stdout.
func runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := gitCmd(ctx, dir, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// commit is one entry of the first-parent history.
type commit struct {
	sha        string
	parent     string // first parent, "" for the root commit
	time       int64  // committer time
	authorTime int64
	author     string
	subject    string
}

// maxSubject caps the stored commit subject (in runes).
const maxSubject = 200

// firstParentLog returns the first-parent history of head, oldest first.
// Fields are separated by \x1f and records by \x1e so that author names and
// subjects can contain anything but those control characters.
func firstParentLog(ctx context.Context, dir, head string) ([]commit, error) {
	out, err := runGit(ctx, dir, "log", "--first-parent", "--reverse", "--no-show-signature",
		"--format=%H%x1f%ct%x1f%P%x1f%at%x1f%an%x1f%s%x1e", head, "--")
	if err != nil {
		return nil, err
	}
	var commits []commit
	for _, rec := range strings.Split(string(out), "\x1e") {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) != 6 {
			return nil, fmt.Errorf("parsing log record %q: %d fields", rec, len(f))
		}
		t, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing log record %q: %w", rec, err)
		}
		at, _ := strconv.ParseInt(f[3], 10, 64)
		c := commit{sha: f[0], time: t, authorTime: at, author: f[4], subject: truncateRunes(f[5], maxSubject)}
		if parents := strings.Fields(f[2]); len(parents) > 0 {
			c.parent = parents[0]
		}
		commits = append(commits, c)
	}
	return commits, nil
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// treeEntry is a file of a tree (ls-tree) or the new side of a diff entry.
type treeEntry struct {
	mode string
	blob string
	path string
}

// isRegularFile filters out submodules (160000), symlinks (120000) and trees.
func isRegularFile(mode string) bool {
	return mode == "100644" || mode == "100755" || mode == "100664"
}

// lsTree lists all the files of a commit.
func lsTree(ctx context.Context, dir, rev string) ([]treeEntry, error) {
	out, err := runGit(ctx, dir, "ls-tree", "-r", "-z", "--full-tree", rev)
	if err != nil {
		return nil, err
	}
	var entries []treeEntry
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		// "<mode> SP <type> SP <object> TAB <file>"
		meta, p, ok := bytes.Cut(rec, []byte{'\t'})
		if !ok {
			return nil, fmt.Errorf("unexpected ls-tree record %q", rec)
		}
		f := strings.Fields(string(meta))
		if len(f) != 3 {
			return nil, fmt.Errorf("unexpected ls-tree record %q", rec)
		}
		entries = append(entries, treeEntry{mode: f[0], blob: f[2], path: string(p)})
	}
	return entries, nil
}

// diffEntry is one change of a commit against its first parent.
type diffEntry struct {
	status  byte // A, D, M, T
	oldMode string
	newMode string
	oldBlob string
	newBlob string
	path    string
}

// diffStream wraps a long running `git diff-tree --stdin` process. Pairs are
// written by a goroutine and the output is consumed commit by commit, in the
// same order.
type diffStream struct {
	cmd     *exec.Cmd
	out     *bufio.Reader
	stderr  bytes.Buffer
	pending []byte // token read ahead (next commit header)
	done    chan error

	closeOnce sync.Once
	closeErr  error
}

func startDiffStream(ctx context.Context, dir string, commits []commit) (*diffStream, error) {
	// --always prints the commit header even when the diff is empty, so the
	// output can be consumed commit by commit in lockstep with the input.
	cmd := gitCmd(ctx, dir, "diff-tree", "--stdin", "-r", "-z", "--no-renames", "--root", "--always")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	s := &diffStream{cmd: cmd, out: bufio.NewReaderSize(stdout, 256*1024), done: make(chan error, 1)}
	cmd.Stderr = &s.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		w := bufio.NewWriter(stdin)
		var err error
		for _, c := range commits {
			if c.parent == "" {
				_, err = fmt.Fprintf(w, "%s\n", c.sha)
			} else {
				_, err = fmt.Fprintf(w, "%s %s\n", c.sha, c.parent)
			}
			if err != nil {
				break
			}
		}
		if err == nil {
			err = w.Flush()
		}
		stdin.Close()
		s.done <- err
	}()
	return s, nil
}

func (s *diffStream) token() ([]byte, error) {
	if s.pending != nil {
		t := s.pending
		s.pending = nil
		return t, nil
	}
	t, err := s.out.ReadBytes(0)
	if err != nil {
		if err == io.EOF && len(t) == 0 {
			return nil, io.EOF
		}
		if err == io.EOF {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return t[:len(t)-1], nil
}

// next reads the changes of the next commit, which must be sha.
func (s *diffStream) next(sha string) ([]diffEntry, error) {
	header, err := s.token()
	if err != nil {
		return nil, fmt.Errorf("diff-tree: reading header for %s: %w (%s)", sha, err, strings.TrimSpace(s.stderr.String()))
	}
	if string(header) != sha {
		return nil, fmt.Errorf("diff-tree: expected commit %s, got %q", sha, header)
	}
	var entries []diffEntry
	for {
		tok, err := s.token()
		if err == io.EOF {
			return entries, nil
		}
		if err != nil {
			return nil, fmt.Errorf("diff-tree: %w", err)
		}
		if len(tok) == 0 || tok[0] != ':' {
			s.pending = tok // header of the next commit
			return entries, nil
		}
		// ":<old mode> <new mode> <old sha> <new sha> <status>" NUL "<path>" NUL
		f := strings.Fields(string(tok[1:]))
		if len(f) != 5 || len(f[4]) == 0 {
			return nil, fmt.Errorf("diff-tree: unexpected record %q", tok)
		}
		p, err := s.token()
		if err != nil {
			return nil, fmt.Errorf("diff-tree: reading path: %w", err)
		}
		entries = append(entries, diffEntry{
			status:  f[4][0],
			oldMode: f[0],
			newMode: f[1],
			oldBlob: f[2],
			newBlob: f[3],
			path:    string(p),
		})
	}
}

// close drains the output and waits for the process. It is idempotent.
func (s *diffStream) close() error {
	s.closeOnce.Do(func() {
		io.Copy(io.Discard, s.out)
		werr := <-s.done
		if err := s.cmd.Wait(); err != nil {
			s.closeErr = fmt.Errorf("diff-tree: %w: %s", err, strings.TrimSpace(s.stderr.String()))
			return
		}
		s.closeErr = werr
	})
	return s.closeErr
}

// catFile wraps a long running `git cat-file --batch-command` process, which
// lets us ask for the size first and skip the content of huge blobs.
type catFile struct {
	cmd    *exec.Cmd
	in     *bufio.Writer
	stdin  io.Closer
	out    *bufio.Reader
	stderr bytes.Buffer
}

func startCatFile(ctx context.Context, dir string) (*catFile, error) {
	cmd := gitCmd(ctx, dir, "cat-file", "--batch-command")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	c := &catFile{cmd: cmd, in: bufio.NewWriter(stdin), stdin: stdin, out: bufio.NewReaderSize(stdout, 256*1024)}
	cmd.Stderr = &c.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *catFile) command(cmd, sha string) (size int64, err error) {
	if _, err := fmt.Fprintf(c.in, "%s %s\n", cmd, sha); err != nil {
		return 0, err
	}
	if err := c.in.Flush(); err != nil {
		return 0, err
	}
	line, err := c.out.ReadString('\n')
	if err != nil {
		return 0, fmt.Errorf("cat-file: %w: %s", err, strings.TrimSpace(c.stderr.String()))
	}
	// "<sha> <type> <size>" or "<sha> missing"
	f := strings.Fields(line)
	if len(f) != 3 {
		return 0, fmt.Errorf("cat-file: object %s: %s", sha, strings.TrimSpace(line))
	}
	return strconv.ParseInt(f[2], 10, 64)
}

// size returns the size of a blob without reading it.
func (c *catFile) size(sha string) (int64, error) {
	return c.command("info", sha)
}

// contents returns the content of a blob.
func (c *catFile) contents(sha string) ([]byte, error) {
	size, err := c.command("contents", sha)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, size+1) // content + trailing LF
	if _, err := io.ReadFull(c.out, buf); err != nil {
		return nil, fmt.Errorf("cat-file: reading %s: %w", sha, err)
	}
	return buf[:size], nil
}

func (c *catFile) close() error {
	c.stdin.Close()
	io.Copy(io.Discard, c.out)
	return c.cmd.Wait()
}
