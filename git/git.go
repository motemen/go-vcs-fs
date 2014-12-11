package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/tools/godoc/vfs"
)

type Repository struct {
	GitDir   string
	Revision string

	treeCache map[string]map[string]*treeEntry // dir -> path -> entry
}

func NewRepository(revision, gitDir string) (*Repository, error) {
	if revision == "" {
		revision = "HEAD"
	}

	if gitDir == "" {
		out, err := git("rev-parse", "--git-dir")
		if err != nil {
			return nil, err
		}

		gitDir, err = out.first()
		if err != nil {
			return nil, err
		}
	}

	return &Repository{
		Revision: revision,
		GitDir:   gitDir,
	}, nil
}

// implements os.FileInfo
type treeEntry struct {
	parent  string
	name    string
	objType uint16
	mode    uint16
	sha1    string
	size    int64 // only meaningful if objectType == "blob"
	repo    *Repository
}

const (
	objTypeDir     = 0040
	objTypeRegular = 0100
	objTypeSymlink = 0120
	objTypeGitlink = 0160
)

func (e treeEntry) IsDir() bool {
	return e.objType == objTypeDir
}

func (e treeEntry) ModTime() time.Time {
	dateOutput, _ := e.repo.git("log", "-1", "--pretty=format:%aD")
	date, _ := dateOutput.first()
	lastMod, _ := time.Parse(time.RFC1123Z, date)
	return lastMod
}

func (e treeEntry) Mode() os.FileMode {
	return os.FileMode(e.mode)
}

func (e treeEntry) Name() string     { return e.name }
func (e treeEntry) Size() int64      { return e.size }
func (e treeEntry) Sys() interface{} { return nil }

func (e treeEntry) Path() string {
	return path.Join(e.parent, e.name)
}

type output struct {
	*bytes.Buffer
}

func (o output) first() (string, error) {
	b, err := o.ReadString('\n')
	if err != nil {
		return "", err
	}

	return strings.TrimRight(string(b), "\n"), nil
}

func (o output) lines(ch byte) ([]string, error) {
	return strings.Split(o.String(), string([]byte{ch})), nil
}

func (repo *Repository) git(args ...string) (*output, error) {
	gitArgs := args
	if repo.GitDir != "" {
		gitArgs = append([]string{"--git-dir=" + repo.GitDir}, args...)
	}

	return git(gitArgs...)
}

func git(args ...string) (*output, error) {
	stderr := new(bytes.Buffer)
	cmd := exec.Command("git", args...)
	cmd.Stderr = stderr
	out, err := cmd.Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s: %q", err, stderr.String())
		} else {
			return nil, err
		}
	}

	return &output{bytes.NewBuffer(out)}, nil
}

func (repo *Repository) revision() string {
	if repo.Revision != "" {
		return repo.Revision
	}

	return "HEAD"
}

var rxLsTreeLine = regexp.MustCompile(`^(?P<mode>[0-7]{6}) +(?P<type>\S+) +(?P<sha1>[0-9a-f]{40}) +(?P<size>\d+|-)\t(?P<name>.+)$`)

// example output:
//   040000 tree d564d0bc3dd917926892c55e3706cc116d5b165e    directory
//   100755 blob e69de29bb2d1d6434b8b29ae775ad8c2e48c5391    executable
//   100644 blob 78981922613b2afb6025042ff6bd878ac1994e85    file
//   160000 commit 5499f342043544dcc4c437c0eb10b4d721f30dd3  submodule
//   120000 blob 8d14cbf983b3fad683171c9418998d9f68340823    symlink
func (repo *Repository) lsTree(path string) (map[string]*treeEntry, error) {
	path = strings.TrimRight(path, "/")
	if path == "." {
		path = ""
	}

	if repo.treeCache == nil {
		repo.treeCache = map[string]map[string]*treeEntry{}
	}

	if cached, ok := repo.treeCache[path]; ok {
		return cached, nil
	}

	out, err := repo.git("ls-tree", "--full-tree", "-z", "-l", repo.revision()+":"+path)
	if err != nil {
		return nil, err
	}

	tree := map[string]*treeEntry{}

	lines, err := out.lines('\x00')
	if err != nil {
		return nil, err
	}

	for _, line := range lines {
		if line == "" {
			continue
		}

		parts := rxLsTreeLine.FindStringSubmatch(line)
		if parts == nil {
			return nil, fmt.Errorf("could not parse line: %q", line)
		}

		var size int64
		modeStr, _, sha1, sizeStr, name := parts[1], parts[2], parts[3], parts[4], parts[5]
		if sizeStr != "-" {
			size, _ = strconv.ParseInt(sizeStr, 10, 64)
		}

		objType, _ := strconv.ParseUint(modeStr[0:3], 8, 16)
		mode, _ := strconv.ParseUint(modeStr[3:6], 8, 16)

		tree[name] = &treeEntry{
			parent:  path,
			name:    name,
			size:    size,
			objType: uint16(objType),
			mode:    uint16(mode),
			sha1:    sha1,
			repo:    repo,
		}
	}

	repo.treeCache[path] = tree

	return tree, nil
}

func (repo *Repository) Lstat(path string) (os.FileInfo, error) {
	e, err := repo.lstat(path)
	if err != nil {
		return nil, err
	}
	return e, nil
}

// TODO: follow symlinks
func (repo *Repository) Stat(path string) (os.FileInfo, error) {
	e, err := repo.stat(path)
	if err != nil {
		return nil, err
	}
	return e, nil
}

func (repo *Repository) lstat(name string) (*treeEntry, error) {
	if name == "." || name == "" {
		treeRevOutput, err := repo.git("rev-parse", repo.revision()+"^{tree}")
		if err != nil {
			return nil, err
		}

		sha1, err := treeRevOutput.first()
		if err != nil {
			return nil, err
		}

		return &treeEntry{
			objType: objTypeDir,
			sha1:    sha1,
			repo:    repo,
		}, nil
	}

	name = strings.TrimRight(name, "/")

	dir, filename := path.Split(name)
	entries, err := repo.lsTree(dir)
	if err != nil {
		return nil, err
	}

	if e, ok := entries[filename]; ok {
		return e, nil
	}

	return nil, fmt.Errorf("file not found: %s", name)
}

func (repo *Repository) stat(path string) (*treeEntry, error) {
	return repo.lstat(path)
}

func (repo *Repository) String() string {
	return fmt.Sprintf("git[rev=%s]", repo.revision())
}

type byName []os.FileInfo

func (x byName) Len() int           { return len(x) }
func (x byName) Swap(i, j int)      { x[i], x[j] = x[j], x[i] }
func (x byName) Less(i, j int) bool { return x[i].Name() < x[j].Name() }

func (repo *Repository) ReadDir(path string) ([]os.FileInfo, error) {
	entryMap, err := repo.lsTree(path)
	if err != nil {
		return nil, err
	}

	entries := []os.FileInfo{}
	for _, e := range entryMap {
		entries = append(entries, e)
	}

	sort.Sort(byName(entries))

	return entries, nil
}

type blob struct {
	*bytes.Reader
}

func (b blob) Close() error { return nil }

func (repo *Repository) Open(path string) (vfs.ReadSeekCloser, error) {
	fi, err := repo.stat(path)
	if err != nil {
		return nil, err
	}
	if fi.objType != objTypeRegular {
		return nil, fmt.Errorf("not a regular blob")
	}

	out, err := repo.git("cat-file", "blob", fi.sha1)
	if err != nil {
		return nil, err
	}

	return blob{bytes.NewReader(out.Bytes())}, nil
}
