package sourceserver

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"connectrpc.com/connect"
	v1 "github.com/grafana/pyroscope/api/gen/proto/go/vcs/v1"
	"github.com/grafana/pyroscope/api/gen/proto/go/vcs/v1/vcsv1connect"
)

func New(repoBasePath string, logger *slog.Logger) vcsv1connect.VCSServiceHandler {
	return &vcsServiceServer{
		repoBasePath: repoBasePath,
		logger:       logger,
		repos:        make(map[string]*repo),
	}
}

type vcsServiceServer struct {
	vcsv1connect.UnimplementedVCSServiceHandler
	repoBasePath string
	logger       *slog.Logger

	repos map[string]*repo
	mu    sync.Mutex
}

func (s *vcsServiceServer) ensureRepo(ctx context.Context, repoURL string) (*repo, error) {
	r := s.getRepo(repoURL)

	return r, r.fetch(ctx)
}

func (s *vcsServiceServer) GetFile(ctx context.Context, req *connect.Request[v1.GetFileRequest]) (*connect.Response[v1.GetFileResponse], error) {
	s.logger.Debug("GetFile", "repo", req.Msg.RepositoryURL, "ref", req.Msg.Ref, "localPath", req.Msg.LocalPath)

	localPath := req.Msg.LocalPath
	repoURL := req.Msg.RepositoryURL
	ref := req.Msg.Ref

	// if go std library, check if the file exists in the repo
	goRootPrefix := "$GOROOT/"
	if strings.HasPrefix(localPath, goRootPrefix) {
		ref = "go1.23.4"
		localPath = strings.TrimPrefix(localPath, goRootPrefix)
		repoURL = "https://github.com/golang/go"
	} else if strings.HasPrefix(localPath, "external/") {
		// if bazel file, check if the file exists in the repo
		panic("bazel files are not supported yet")
	}

	r := s.getRepo(repoURL)
	content, err := r.showFile(ctx, ref, localPath)
	if err != nil {
		return nil, err
	}

	// TODO: Build github URL for the file
	return connect.NewResponse(&v1.GetFileResponse{Content: content}), nil
}

func (s *vcsServiceServer) GetCommit(ctx context.Context, req *connect.Request[v1.GetCommitRequest]) (*connect.Response[v1.GetCommitResponse], error) {
	repo := s.getRepo(req.Msg.RepositoryURL)
	commits, err := repo.showCommits(ctx, req.Msg.Ref)
	if err != nil {
		return nil, err
	}
	if len(commits) == 0 {
		return nil, fmt.Errorf("no commits found for %s", req.Msg.Ref)
	}
	return &connect.Response[v1.GetCommitResponse]{Msg: &v1.GetCommitResponse{
		Message: commits[0].Message,
		Author:  commits[0].Author,
		Date:    commits[0].Date,
		Sha:     commits[0].Sha,
	}}, nil
}

func (s *vcsServiceServer) GetCommits(ctx context.Context, req *connect.Request[v1.GetCommitsRequest]) (*connect.Response[v1.GetCommitsResponse], error) {
	repo := s.getRepo(req.Msg.RepositoryUrl)
	commits, err := repo.showCommits(ctx, req.Msg.Refs...)
	if err != nil {
		return nil, err
	}
	return &connect.Response[v1.GetCommitsResponse]{Msg: &v1.GetCommitsResponse{
		Commits: commits,
	}}, nil
}
