package analyzer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/holacloud/clocme/model"
)

// testRepo is a throwaway non-bare git repository driven by the git binary.
type testRepo struct {
	t     *testing.T
	dir   string
	clock time.Time
}

func newTestRepo(t *testing.T) *testRepo {
	t.Helper()
	r := &testRepo{t: t, dir: t.TempDir(), clock: time.Unix(1700000000, 0)}
	r.git("init", "-q", "-b", "main")
	return r
}

func (r *testRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	date := r.clock.Format(time.RFC3339)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *testRepo) write(p, content string) {
	r.t.Helper()
	full := filepath.Join(r.dir, p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *testRepo) commit(msg string) string {
	r.t.Helper()
	r.clock = r.clock.Add(time.Hour)
	r.git("add", "-A")
	r.git("commit", "-q", "--allow-empty", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

func (r *testRepo) url() string { return "file://" + r.dir }

const mainGo = `package main

// hello
func main() {
}
`

const otherGo = `package main

func other() int {
	return 1
}
`

const appJs = `// app
function app() {
  return 1;
}
`

const featurePy = `# feature
def feature():
    return 1
`

// buildHistory creates the reference history and returns the first-parent
// commit shas, in order.
func buildHistory(r *testRepo) []string {
	var shas []string

	// 1. Go file, README (documentation) and a symlink (ignored)
	r.write("main.go", mainGo)
	r.write("README.md", "# readme\n\nhello\n")
	if err := os.Symlink("main.go", filepath.Join(r.dir, "link.go")); err != nil {
		r.t.Fatal(err)
	}
	shas = append(shas, r.commit("c1"))

	// 2. JS file plus node_modules (vendored) and a file with spaces
	r.write("web/app.js", appJs)
	r.write("node_modules/x/index.js", appJs+appJs)
	r.write("my file.go", otherGo)
	shas = append(shas, r.commit("c2"))

	// 3. modify main.go
	r.write("main.go", mainGo+"\nfunc extra() {}\n")
	shas = append(shas, r.commit("c3"))

	// 4. lib/ marked as vendored with .gitattributes
	r.write("lib/big.go", otherGo+otherGo)
	r.write(".gitattributes", "lib/** linguist-vendored\n*.txt text\n")
	shas = append(shas, r.commit("c4"))

	// 5. feature branch + merge commit
	r.git("checkout", "-q", "-b", "feature")
	r.write("feature.py", featurePy)
	r.commit("feature") // not in first-parent history
	r.git("checkout", "-q", "main")
	r.write("other.go", otherGo)
	shas = append(shas, r.commit("c5"))
	r.clock = r.clock.Add(time.Hour)
	r.git("merge", "-q", "--no-ff", "-m", "merge feature", "feature")
	shas = append(shas, r.git("rev-parse", "HEAD"))

	return shas
}

func newTestAnalyzer(t *testing.T) *Analyzer {
	base := t.TempDir()
	return New(Config{ReposDir: filepath.Join(base, "repos"), CacheDir: filepath.Join(base, "cache")})
}

func langs(p model.Point) map[string]model.Stats { return p.Languages }

func TestAnalyzeHistory(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	shas := buildHistory(r)

	a := newTestAnalyzer(t)
	head, err := a.Sync(ctx, "test~o~r", r.url(), "main")
	if err != nil {
		t.Fatal(err)
	}
	if head != shas[len(shas)-1] {
		t.Fatalf("head = %s, want %s", head, shas[len(shas)-1])
	}

	var calls int
	points, err := a.Analyze(ctx, "test~o~r", head, nil, func(done, total int) { calls++ })
	if err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Error("progress never called")
	}
	if len(points) != len(shas) {
		t.Fatalf("got %d points, want %d", len(points), len(shas))
	}
	for i, p := range points {
		if p.Author != "test" || p.Date == 0 || (p.Message != "c"+strconv.Itoa(i+1) && p.Message != "merge feature") {
			t.Errorf("point %d metadata: author=%q date=%d message=%q", i, p.Author, p.Date, p.Message)
		}
		if p.Commit != shas[i] {
			t.Errorf("point %d commit %s, want %s", i, p.Commit, shas[i])
		}
		if i > 0 && p.Time < points[i-1].Time {
			t.Errorf("point %d time not monotonic", i)
		}
	}

	goMain := model.Stats{Code: 3, Comments: 1, Blanks: 1, Files: 1}
	goOther := model.Stats{Code: 4, Comments: 0, Blanks: 1, Files: 1}
	sum := func(ss ...model.Stats) model.Stats {
		var t model.Stats
		for _, s := range ss {
			t.Add(s)
		}
		return t
	}

	expect := []map[string]model.Stats{
		{"Go": goMain}, // README and symlink not counted
		{"Go": sum(goMain, goOther), "JavaScript": {Code: 3, Comments: 1, Files: 1}},
		{"Go": sum(goMain, goOther, model.Stats{Code: 1, Blanks: 1}), "JavaScript": {Code: 3, Comments: 1, Files: 1}},
		{"Go": sum(goMain, goOther, model.Stats{Code: 1, Blanks: 1}), "JavaScript": {Code: 3, Comments: 1, Files: 1}}, // lib/ vendored
		{"Go": sum(goMain, goOther, goOther, model.Stats{Code: 1, Blanks: 1}), "JavaScript": {Code: 3, Comments: 1, Files: 1}},
		{"Go": sum(goMain, goOther, goOther, model.Stats{Code: 1, Blanks: 1}), "JavaScript": {Code: 3, Comments: 1, Files: 1}, "Python": {Code: 2, Comments: 1, Files: 1}},
	}
	for i, want := range expect {
		if got := langs(points[i]); !reflect.DeepEqual(got, want) {
			t.Errorf("point %d:\n got %+v\nwant %+v", i, got, want)
		}
	}
}

func TestAnalyzeGitattributesChange(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	r.write("main.go", mainGo)
	r.write("lib/big.go", otherGo)
	r.commit("c1")
	r.write(".gitattributes", "lib/** linguist-vendored\n")
	r.commit("c2")
	r.write(".gitattributes", "lib/** -linguist-vendored\n*.go linguist-language=Python\n")
	r.commit("c3")
	r.git("rm", "-q", ".gitattributes")
	r.commit("c4")

	a := newTestAnalyzer(t)
	head, err := a.Sync(ctx, "attrs", r.url(), "main")
	if err != nil {
		t.Fatal(err)
	}
	points, err := a.Analyze(ctx, "attrs", head, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	files := func(i int) map[string]int64 {
		out := map[string]int64{}
		for l, s := range points[i].Languages {
			out[l] = s.Files
		}
		return out
	}
	want := []map[string]int64{
		{"Go": 2},
		{"Go": 1},
		{"Python": 2},
		{"Go": 2},
	}
	for i, w := range want {
		if got := files(i); !reflect.DeepEqual(got, w) {
			t.Errorf("point %d files %v, want %v", i, got, w)
		}
	}
}

func TestAnalyzeIncremental(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	buildHistory(r)

	a := newTestAnalyzer(t)
	head, err := a.Sync(ctx, "inc", r.url(), "main")
	if err != nil {
		t.Fatal(err)
	}
	prev, err := a.Analyze(ctx, "inc", head, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// New commits upstream: delete JS, change attributes, add a file
	r.git("rm", "-q", "web/app.js")
	r.commit("c6")
	r.write(".gitattributes", "*.txt text\n") // lib/ is counted again
	r.write("new.py", featurePy)
	r.commit("c7")

	head2, err := a.Sync(ctx, "inc", r.url(), "main")
	if err != nil {
		t.Fatal(err)
	}
	if head2 == head {
		t.Fatal("sync did not fetch new commits")
	}
	incremental, err := a.Analyze(ctx, "inc", head2, prev, nil)
	if err != nil {
		t.Fatal(err)
	}

	fresh := newTestAnalyzer(t)
	if _, err := fresh.Sync(ctx, "inc", r.url(), "main"); err != nil {
		t.Fatal(err)
	}
	full, err := fresh.Analyze(ctx, "inc", head2, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(incremental, full) {
		t.Fatalf("incremental != full\nincremental: %+v\nfull: %+v", incremental, full)
	}
	if len(full) != len(prev)+2 {
		t.Fatalf("got %d points, want %d", len(full), len(prev)+2)
	}
	if _, ok := full[len(full)-1].Languages["JavaScript"]; ok {
		t.Error("JavaScript should be gone")
	}

	// Unchanged head: same result
	again, err := a.Analyze(ctx, "inc", head2, incremental, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, full) {
		t.Fatal("re-analysis of the same head changed the result")
	}

	// Rewritten history (prev not a prefix): full recompute
	bogus := append([]model.Point{{Commit: "0000000000000000000000000000000000000000"}}, prev[1:]...)
	recomputed, err := a.Analyze(ctx, "inc", head2, bogus, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recomputed, full) {
		t.Fatal("recompute after history rewrite differs from full analysis")
	}
}

func TestSyncErrors(t *testing.T) {
	ctx := context.Background()
	a := newTestAnalyzer(t)
	if _, err := a.Sync(ctx, "../evil", "file:///nope", "main"); err == nil {
		t.Error("expected invalid id error")
	}
	if _, err := a.Sync(ctx, "x", "file:///nope", "--upload-pack=evil"); err == nil {
		t.Error("expected invalid branch error")
	}
	if _, err := a.Sync(ctx, "x", "file://"+t.TempDir()+"/missing", "main"); err == nil {
		t.Error("expected clone error")
	}
	if _, err := os.Stat(filepath.Join(a.config.ReposDir, "x.git")); !os.IsNotExist(err) {
		t.Error("failed clone left a repo behind")
	}
}
