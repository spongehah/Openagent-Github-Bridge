package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var ownerOrRepoSegment = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type config struct {
	Addr         string
	RepoRoot     string
	WorktreeRoot string
	BaseRemote   string
	Username     string
	Password     string
}

type worktreeRequest struct {
	Owner   string `json:"owner"`
	Repo    string `json:"repo"`
	Kind    string `json:"kind"`
	Number  int    `json:"number"`
	Branch  string `json:"branch"`
	BaseRef string `json:"baseRef"`
	HeadSHA string `json:"headSHA,omitempty"`
	Force   bool   `json:"force,omitempty"`
}

type worktreeResponse struct {
	Key          string `json:"key"`
	Kind         string `json:"kind"`
	Branch       string `json:"branch"`
	BaseRef      string `json:"baseRef"`
	WorktreePath string `json:"worktreePath"`
	Reused       bool   `json:"reused"`
}

type worktreeInfo struct {
	Path   string
	Branch string
	Head   string
}

type worktreeState struct {
	ByPath   *worktreeInfo
	ByBranch *worktreeInfo
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	svc := &service{cfg: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", svc.handleHealth)
	mux.HandleFunc("/worktrees/create-or-reuse", svc.handleCreateOrReuse)

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           svc.withAuth(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("worktree-manager listening on %s for repo %s (base remote: %s)", cfg.Addr, cfg.RepoRoot, cfg.BaseRemote)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("worktree-manager server failed: %v", err)
	}
}

type service struct {
	cfg config
}

func (s *service) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Password == "" {
			next.ServeHTTP(w, r)
			return
		}

		username, password, ok := r.BasicAuth()
		if !ok || username != s.cfg.Username || password != s.cfg.Password {
			w.Header().Set("WWW-Authenticate", `Basic realm="worktree-manager"`)
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *service) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "ok",
		"repoRoot":     s.cfg.RepoRoot,
		"worktreeRoot": s.cfg.WorktreeRoot,
		"baseRemote":   s.cfg.BaseRemote,
	})
}

func (s *service) handleCreateOrReuse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req worktreeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
		return
	}

	result, err := s.createOrReuse(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *service) createOrReuse(ctx context.Context, req worktreeRequest) (*worktreeResponse, error) {
	owner, err := validatePathSegment("owner", req.Owner)
	if err != nil {
		return nil, err
	}
	repo, err := validatePathSegment("repo", req.Repo)
	if err != nil {
		return nil, err
	}
	if req.Number <= 0 {
		return nil, fmt.Errorf("number must be positive")
	}
	if req.Kind != "issue" && req.Kind != "pr_review" {
		return nil, fmt.Errorf("kind must be issue or pr_review")
	}

	branch := strings.TrimSpace(req.Branch)
	baseRef := strings.TrimSpace(req.BaseRef)
	headSHA := strings.TrimSpace(req.HeadSHA)
	if baseRef == "" {
		return nil, fmt.Errorf("baseRef is required")
	}
	if err := validateBranchName(ctx, s.cfg.RepoRoot, branch); err != nil {
		return nil, err
	}
	if req.Kind == "issue" && headSHA != "" {
		return nil, fmt.Errorf("headSHA is only supported for pr_review worktrees")
	}

	key := fmt.Sprintf("%s/%s/%s/%d", owner, repo, req.Kind, req.Number)
	worktreePath := filepath.Join(s.cfg.WorktreeRoot, owner, repo, worktreeDirectoryName(req.Kind, req.Number))

	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create managed worktree root: %w", err)
	}

	if _, err := runGit(ctx, s.cfg.RepoRoot, "fetch", "--all", "--prune"); err != nil {
		return nil, err
	}
	existing, err := getWorktreeState(ctx, s.cfg.RepoRoot, worktreePath, branch)
	if err != nil {
		return nil, err
	}
	if existing.ByBranch != nil && existing.ByBranch.Path != filepath.Clean(worktreePath) {
		return nil, fmt.Errorf("branch %s is already checked out in another worktree: %s", branch, existing.ByBranch.Path)
	}

	if req.Force {
		if err := removeManagedWorktree(ctx, s.cfg.RepoRoot, s.cfg.WorktreeRoot, worktreePath, existing.ByPath); err != nil {
			return nil, err
		}
	} else {
		if existing.ByPath != nil {
			if req.Kind == "pr_review" && headSHA != "" {
				if err := refreshPRWorktree(ctx, s.cfg.RepoRoot, existing.ByPath.Path, branch, headSHA, runGit); err != nil {
					return nil, err
				}
			}
			return &worktreeResponse{
				Key:          key,
				Kind:         req.Kind,
				Branch:       coalesce(existing.ByPath.Branch, branch),
				BaseRef:      baseRef,
				WorktreePath: existing.ByPath.Path,
				Reused:       true,
			}, nil
		}
		if _, err := os.Stat(worktreePath); err == nil {
			return nil, fmt.Errorf("managed worktree path already exists but is not registered with git: %s", worktreePath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("failed to inspect managed worktree path: %w", err)
		}
	}

	if err := addWorktreeForRequest(ctx, s.cfg.RepoRoot, worktreePath, branch, baseRef, headSHA, s.cfg.BaseRemote, runGit); err != nil {
		return nil, err
	}

	return &worktreeResponse{
		Key:          key,
		Kind:         req.Kind,
		Branch:       branch,
		BaseRef:      baseRef,
		WorktreePath: filepath.Clean(worktreePath),
		Reused:       false,
	}, nil
}

func loadConfig() (config, error) {
	addr := firstNonEmpty(os.Getenv("WORKTREE_MANAGER_ADDR"), ":4081")
	repoRoot := strings.TrimSpace(os.Getenv("WORKTREE_MANAGER_REPO_ROOT"))
	if repoRoot == "" {
		return config{}, fmt.Errorf("WORKTREE_MANAGER_REPO_ROOT is required")
	}

	normalizedRepoRoot, err := normalizeRepoRoot(repoRoot)
	if err != nil {
		return config{}, err
	}

	worktreeRoot := strings.TrimSpace(os.Getenv("WORKTREE_MANAGER_ROOT"))
	if worktreeRoot == "" {
		homeDir, err := resolveHomeDir()
		if err != nil {
			return config{}, err
		}
		worktreeRoot = filepath.Join(homeDir, ".opencode", "worktrees")
	}

	baseRemote := firstNonEmpty(os.Getenv("WORKTREE_MANAGER_BASE_REMOTE"), "origin")
	username := firstNonEmpty(os.Getenv("WORKTREE_MANAGER_USERNAME"), "worktree-manager")

	return config{
		Addr:         addr,
		RepoRoot:     normalizedRepoRoot,
		WorktreeRoot: filepath.Clean(worktreeRoot),
		BaseRemote:   strings.TrimSpace(baseRemote),
		Username:     username,
		Password:     os.Getenv("WORKTREE_MANAGER_PASSWORD"),
	}, nil
}

func normalizeRepoRoot(repoRoot string) (string, error) {
	absRepoRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", fmt.Errorf("failed to resolve repo root: %w", err)
	}

	output, err := runGit(context.Background(), absRepoRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}

	return filepath.Clean(strings.TrimSpace(output)), nil
}

func validatePathSegment(label, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if !ownerOrRepoSegment.MatchString(trimmed) {
		return "", fmt.Errorf("%s contains unsupported characters: %s", label, value)
	}
	return trimmed, nil
}

func validateBranchName(ctx context.Context, cwd, branch string) error {
	if strings.TrimSpace(branch) == "" {
		return fmt.Errorf("branch is required")
	}

	_, err := runGit(ctx, cwd, "check-ref-format", "--branch", branch)
	return err
}

func verifyCommitish(ctx context.Context, cwd, ref string) error {
	if _, err := runGit(ctx, cwd, "rev-parse", "--verify", ref+"^{commit}"); err != nil {
		return fmt.Errorf("unable to resolve ref %s to a commit: %w", ref, err)
	}
	return nil
}

type gitRunner func(ctx context.Context, cwd string, args ...string) (string, error)

func addWorktreeForRequest(ctx context.Context, repoRoot, worktreePath, branch, baseRef, headSHA, baseRemote string, run gitRunner) error {
	if strings.TrimSpace(headSHA) != "" {
		return addPRWorktree(ctx, repoRoot, worktreePath, branch, headSHA, run)
	}

	return addIssueWorktree(ctx, repoRoot, worktreePath, branch, baseRef, baseRemote, run)
}

func addPRWorktree(ctx context.Context, repoRoot, worktreePath, branch, headSHA string, run gitRunner) error {
	if err := verifyCommitishWithRunner(ctx, repoRoot, headSHA, run); err != nil {
		return err
	}
	if _, err := run(ctx, repoRoot, "worktree", "add", "-B", branch, worktreePath, headSHA); err != nil {
		return err
	}

	return nil
}

func addIssueWorktree(ctx context.Context, repoRoot, worktreePath, branch, baseRef, baseRemote string, run gitRunner) error {
	remoteRef := qualifyRemoteRef(baseRemote, baseRef)
	if err := verifyCommitishWithRunner(ctx, repoRoot, remoteRef, run); err == nil {
		if _, addErr := run(ctx, repoRoot, "worktree", "add", "-B", branch, worktreePath, remoteRef); addErr == nil {
			return nil
		} else if err := syncRemoteBranchToLocal(ctx, repoRoot, baseRemote, baseRef, run); err != nil {
			return fmt.Errorf("failed to create worktree from %s and failed to sync local %s: %w", remoteRef, baseRef, err)
		}
	} else if err := syncRemoteBranchToLocal(ctx, repoRoot, baseRemote, baseRef, run); err != nil {
		return fmt.Errorf("failed to resolve %s and failed to sync local %s: %w", remoteRef, baseRef, err)
	}

	if err := verifyCommitishWithRunner(ctx, repoRoot, baseRef, run); err != nil {
		return err
	}
	if _, err := run(ctx, repoRoot, "worktree", "add", "-B", branch, worktreePath, baseRef); err != nil {
		return err
	}

	return nil
}

func refreshPRWorktree(ctx context.Context, repoRoot, worktreePath, branch, headSHA string, run gitRunner) error {
	if err := verifyCommitishWithRunner(ctx, repoRoot, headSHA, run); err != nil {
		return err
	}
	if _, err := run(ctx, worktreePath, "reset", "--hard"); err != nil {
		return err
	}
	if _, err := run(ctx, worktreePath, "clean", "-fdx"); err != nil {
		return err
	}
	if _, err := run(ctx, worktreePath, "checkout", "-B", branch, headSHA); err != nil {
		return err
	}
	if _, err := run(ctx, worktreePath, "reset", "--hard", headSHA); err != nil {
		return err
	}
	if _, err := run(ctx, worktreePath, "clean", "-fdx"); err != nil {
		return err
	}

	return nil
}

func qualifyRemoteRef(baseRemote, baseRef string) string {
	trimmedBase := strings.TrimSpace(baseRef)
	trimmedRemote := strings.TrimSpace(baseRemote)
	switch {
	case trimmedBase == "":
		return ""
	case trimmedRemote == "":
		return trimmedBase
	case strings.HasPrefix(trimmedBase, "refs/"):
		return trimmedBase
	case strings.HasPrefix(trimmedBase, trimmedRemote+"/"):
		return trimmedBase
	default:
		return trimmedRemote + "/" + trimmedBase
	}
}

func syncRemoteBranchToLocal(ctx context.Context, repoRoot, baseRemote, baseRef string, run gitRunner) error {
	localBranch, ok := localBranchName(baseRemote, baseRef)
	if !ok {
		return fmt.Errorf("baseRef %s cannot be synced to a local branch", baseRef)
	}
	if strings.TrimSpace(baseRemote) == "" {
		return fmt.Errorf("base remote is required to sync branch %s", localBranch)
	}

	refspec := fmt.Sprintf("%s:refs/heads/%s", localBranch, localBranch)
	if _, err := run(ctx, repoRoot, "fetch", baseRemote, refspec); err != nil {
		return fmt.Errorf("failed to sync %s/%s to local branch %s: %w", baseRemote, localBranch, localBranch, err)
	}

	return nil
}

func localBranchName(baseRemote, baseRef string) (string, bool) {
	trimmedBase := strings.TrimSpace(baseRef)
	trimmedRemote := strings.TrimSpace(baseRemote)
	switch {
	case trimmedBase == "":
		return "", false
	case strings.HasPrefix(trimmedBase, "refs/"):
		return "", false
	case trimmedRemote != "" && strings.HasPrefix(trimmedBase, trimmedRemote+"/"):
		return strings.TrimPrefix(trimmedBase, trimmedRemote+"/"), true
	default:
		return trimmedBase, true
	}
}

func verifyCommitishWithRunner(ctx context.Context, cwd, ref string, run gitRunner) error {
	if _, err := run(ctx, cwd, "rev-parse", "--verify", ref+"^{commit}"); err != nil {
		return fmt.Errorf("unable to resolve ref %s to a commit: %w", ref, err)
	}
	return nil
}

func getWorktreeState(ctx context.Context, repoRoot, targetPath, branch string) (worktreeState, error) {
	output, err := runGit(ctx, repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return worktreeState{}, err
	}

	var state worktreeState
	for _, item := range parseWorktreeList(output) {
		if item.Path == filepath.Clean(targetPath) {
			state.ByPath = item
		}
		if branch != "" && item.Branch == branch {
			state.ByBranch = item
		}
	}

	return state, nil
}

func parseWorktreeList(output string) []*worktreeInfo {
	lines := strings.Split(output, "\n")
	items := make([]*worktreeInfo, 0)
	var current *worktreeInfo

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if current != nil {
				items = append(items, current)
				current = nil
			}
			continue
		}

		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			current = &worktreeInfo{Path: filepath.Clean(value)}
		case "branch":
			if current != nil {
				current.Branch = strings.TrimPrefix(value, "refs/heads/")
			}
		case "HEAD":
			if current != nil {
				current.Head = value
			}
		}
	}

	if current != nil {
		items = append(items, current)
	}

	return items
}

func removeManagedWorktree(ctx context.Context, repoRoot, managedRoot, targetPath string, existing *worktreeInfo) error {
	resolvedManagedRoot := filepath.Clean(managedRoot)
	resolvedTargetPath := filepath.Clean(targetPath)
	if !strings.HasPrefix(resolvedTargetPath, resolvedManagedRoot+string(os.PathSeparator)) {
		return fmt.Errorf("refusing to remove unmanaged worktree path: %s", resolvedTargetPath)
	}

	if existing != nil {
		if _, err := runGit(ctx, repoRoot, "worktree", "remove", "--force", resolvedTargetPath); err != nil {
			return err
		}
	}

	if err := os.RemoveAll(resolvedTargetPath); err != nil {
		return fmt.Errorf("failed to remove managed worktree path %s: %w", resolvedTargetPath, err)
	}

	return nil
}

func runGit(ctx context.Context, cwd string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), detail)
	}

	return string(output), nil
}

func resolveHomeDir() (string, error) {
	if homeDir, err := os.UserHomeDir(); err == nil && homeDir != "" {
		return homeDir, nil
	}

	currentUser, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}
	if currentUser.HomeDir == "" {
		return "", fmt.Errorf("failed to resolve home directory")
	}

	return currentUser.HomeDir, nil
}

func worktreeDirectoryName(kind string, number int) string {
	if kind == "pr_review" {
		return fmt.Sprintf("pr-%d", number)
	}
	return fmt.Sprintf("issue-%d", number)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func coalesce(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
