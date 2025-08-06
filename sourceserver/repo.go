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

type repo struct {
	dir       string
	repo      string
	logger    *slog.Logger
	lastFetch time.Time
	mu        sync.Mutex
}

func newRepo(base, repoURL string, logger *slog.Logger) *repo {
	hash := sha256.Sum256([]byte(repoURL))
	dir := filepath.Join(base, fmt.Sprintf("%x", hash))
	return &repo{
		dir:    dir,
		repo:   repoURL,
		logger: logger,
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

func (r *repo) cmd(ctx context.Context, args ...string) ([]byte, []byte, error) {
	command := append([]string{"git", "--git-dir", r.dir}, args...)
	r.logger.Debug("run command", "command", strings.Join(command, " "))
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = r.dir
	bufO := bytes.Buffer{}
	bufE := bytes.Buffer{}
	cmd.Stdout = &bufO
	cmd.Stderr = &bufE

	if err := cmd.Run(); err != nil {
		return nil, nil, fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, bufE.String())
	}

	return bufO.Bytes(), bufE.Bytes(), nil
}

func (r *repo) showFile(ctx context.Context, ref, localPath string) (string, error) {
	if err := r.fetch(ctx); err != nil {
		return "", err
	}

	stdOut, stdErr, err := r.cmd(ctx, "show", fmt.Sprintf("%s:%s", ref, localPath))
	if err != nil {
		r.logger.Error("git show failed", "error", err, "output", string(stdErr))
		return "", fmt.Errorf("git show file failed: %w", err)
	}

	return string(stdOut), nil
}

func (r *repo) showCommits(ctx context.Context, refs ...string) ([]*v1.CommitInfo, error) {
	if err := r.fetch(ctx); err != nil {
		return nil, err
	}

	stdOut, stdErr, err := r.cmd(ctx, "show", "--format=fuller", "--no-patch", strings.Join(refs, " "))
	if err != nil {
		r.logger.Error("git show failed", "error", err, "output", string(stdErr))
		return nil, fmt.Errorf("git show commits failed: %w", err)
	}

	lines := strings.Split(string(stdOut), "\n")
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
