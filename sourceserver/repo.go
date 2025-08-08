package sourceserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	v1 "github.com/grafana/pyroscope/api/gen/proto/go/vcs/v1"
)

type bazelKey struct {
	ref     string
	sourceRoot string
}

type repo struct {
	dir       string
	repo      string
	logger    *slog.Logger
	lastFetch time.Time
	bazelOutputs map[bazelKey]string
	mu        sync.Mutex
}

func newRepo(base, repoURL string, logger *slog.Logger) *repo {
	hash := sha256.Sum256([]byte(repoURL))
	dir := filepath.Join(base, fmt.Sprintf("%x", hash))
	return &repo{
		dir:    dir,
		repo:   repoURL,
		logger: logger,
		bazelOutputs: make(map[bazelKey]string),
	}
}

func (r *repo) fetch(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if time.Since(r.lastFetch) < 10*time.Minute {
		r.logger.Debug("repo last fetched recently, skipping fetch", "repo", r.repo, "lastFetch", r.lastFetch)
		return nil
	}

	if _, err := os.Stat(r.dir); os.IsNotExist(err) {
		r.logger.Debug("repo not found, cloning", "repo", r.repo, "dir", r.dir)
		cmd := exec.CommandContext(ctx, "git", "clone", "--bare", r.repo, r.dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			r.logger.Error("git clone failed", "error", err, "output", string(out))
			return errors.New("git clone failed")
		}
	} else {
		r.logger.Debug("repo found, fetching", "repo", r.repo, "dir", r.dir)
		cmd := exec.CommandContext(ctx, "git", "--git-dir", r.dir, "fetch")
		if out, err := cmd.CombinedOutput(); err != nil {
			r.logger.Error("git fetch failed", "error", err, "output", string(out))
			return errors.New("git fetch failed")
		}
	}
	r.lastFetch = time.Now()
	return nil
}

func (r *repo) showFile(ctx context.Context, ref, rootPath, localPath string) (string, error) {
	if err := r.fetch(ctx); err != nil {
		return "", err
	}
	localPath = filepath.Join(rootPath, localPath)
	stdOut := &bytes.Buffer{}
	cmd, done := r.cmd(ctx, append(r.gitArgs(), "show", fmt.Sprintf("%s:%s", ref, localPath)), stdOut, nil)
	if err := done("git show", cmd.Run()); err != nil {
		return "", err
	}

	return stdOut.String(), nil
}
func (r *repo) gitArgs() []string {
	return []string{
		"git",
		"--git-dir", r.dir,
	}
}

func (r *repo) bazelArgs() []string {
	return []string{
		"bazel",
	}
}

func (r *repo) cmd(ctx context.Context, cmdString []string, stdOut, stdErr *bytes.Buffer) (*exec.Cmd, func(msg string, err error) error) {
	start := time.Now()
	if stdOut == nil {
		stdOut = &bytes.Buffer{}
	} else {
		stdOut.Reset()
	}
	if stdErr == nil {
		stdErr = &bytes.Buffer{}
	} else {
		stdErr.Reset()
	}
	cmd := exec.CommandContext(ctx, cmdString[0], cmdString[1:]...)	
	cmd.Stdout = stdOut
	cmd.Stderr = stdErr
	return cmd, func(msg string, err error) error {
		if err != nil {
			r.logger.Error(msg+" failed", "command", strings.Join(cmdString, " "), "duration", time.Since(start), "error", err, "output", stdOut.String(), "stderr", stdErr.String())
			return err
		}
		r.logger.Debug(msg+" succeeded", "command", strings.Join(cmdString, " "), "duration", time.Since(start), "error", err, "output", stdOut.String(), "stderr", stdErr.String())
		return nil
	}
}

func (r *repo) prepareBazel(ctx context.Context, ref, sourceRoot string) (string, error) {
	k := bazelKey{
		ref: ref,
		sourceRoot: sourceRoot,
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if outputBase, ok := r.bazelOutputs[k]; ok {
		return outputBase, nil
	}
	// create temp dir
	tempDir, err := os.MkdirTemp("", "pyroscope-bazel-repo")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	stdOut := &bytes.Buffer{}
	stdErr := &bytes.Buffer{}
	cmd, done := r.cmd(ctx, append(r.gitArgs(),"--work-tree", tempDir, "checkout", ref, "."), stdOut, stdErr)
	if err := done("git checkout", cmd.Run()); err != nil {
		return "", err
	}
	
	// now run bazel fetch
	workDir := filepath.Join(tempDir, sourceRoot)
	cmd, done = r.cmd(ctx, append(r.bazelArgs(), "fetch", "//..."), stdOut, stdErr)
	cmd.Dir = workDir
	if err := done("bazel fetch", cmd.Run()); err != nil {
		return "", err
	}

	// now get the build dir
	cmd, done = r.cmd(ctx, append(r.bazelArgs(), "info","output_base"), stdOut, stdErr)
	cmd.Dir = workDir
	if err := done("get bazel output_base", cmd.Run()); err != nil {
		return "", err
	}
	outputBase := strings.TrimSpace(stdOut.String())
	r.bazelOutputs[k] = outputBase

	return outputBase, nil

}

func (r *repo) showBazelFile(ctx context.Context, ref, rootPath, localPath string) (string, error) {
	if err := r.fetch(ctx); err != nil {
		return "", err
	}

	outputBase, err := r.prepareBazel(ctx, ref, rootPath)
	if err != nil {
		return "", err
	}

	// TODO: This needs some serious checks to avoid ath traversal attack

	r.logger.Debug("bazel output base", "outputBase", outputBase)

	content, err := os.ReadFile(filepath.Join(outputBase, localPath))
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func (r *repo) showCommits(ctx context.Context, refs ...string) ([]*v1.CommitInfo, error) {
	if err := r.fetch(ctx); err != nil {
		return nil, err
	}

	stdOut := &bytes.Buffer{}
	cmd, done := r.cmd(ctx, append(r.gitArgs(), "show", "--format=fuller", "--no-patch", strings.Join(refs, " ")), stdOut, nil)
	if err := done("git show", cmd.Run()); err != nil {
		return nil, err
	}

	lines := strings.Split(stdOut.String(), "\n")
	commits := make([]*v1.CommitInfo, 0, len(lines))
	for _, line := range lines {
		commits = append(commits, &v1.CommitInfo{
			Sha: line,
		})
	}
	return commits, nil
}

func (s *vcsServiceServer) getRepo(repoURL string) *repo {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.repos[repoURL]
	if !ok {
		r = newRepo(s.repoBasePath, repoURL, s.logger)
		s.repos[repoURL] = r
	}
	return r
}
