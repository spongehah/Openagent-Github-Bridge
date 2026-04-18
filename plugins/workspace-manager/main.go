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
	Addr          string
	WorkspaceRoot string
	BaseRemote    string
	Username      string
	Password      string
}

type workspaceRequest struct {
	Owner   string `json:"owner"`
	Repo    string `json:"repo"`
	RepoURL string `json:"repoURL"`
	Kind    string `json:"kind"`
	Number  int    `json:"number"`
	Branch  string `json:"branch"`
	BaseRef string `json:"baseRef"`
	HeadSHA string `json:"headSHA,omitempty"`
	Force   bool   `json:"force,omitempty"`
}

type workspaceResponse struct {
	Key          string `json:"key"`
	Kind         string `json:"kind"`
	Branch       string `json:"branch"`
	BaseRef      string `json:"baseRef"`
	WorktreePath string `json:"worktreePath"`
	Reused       bool   `json:"reused"`
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	svc := &service{cfg: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", svc.handleHealth)
	mux.HandleFunc("/workspaces/create-or-reuse", svc.handleCreateOrReuse)

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           svc.withAuth(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("workspace-manager listening on %s (workspace root: %s, base remote: %s)", cfg.Addr, cfg.WorkspaceRoot, cfg.BaseRemote)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("workspace-manager server failed: %v", err)
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
			w.Header().Set("WWW-Authenticate", `Basic realm="workspace-manager"`)
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
		"status":        "ok",
		"workspaceRoot": s.cfg.WorkspaceRoot,
		"baseRemote":    s.cfg.BaseRemote,
	})
}

func (s *service) handleCreateOrReuse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req workspaceRequest
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

func (s *service) createOrReuse(ctx context.Context, req workspaceRequest) (*workspaceResponse, error) {
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
	repoURL := strings.TrimSpace(req.RepoURL)
	if baseRef == "" {
		return nil, fmt.Errorf("baseRef is required")
	}
	if repoURL == "" {
		return nil, fmt.Errorf("repoURL is required")
	}
	if err := validateBranchName(ctx, ".", branch); err != nil {
		return nil, err
	}

	key := fmt.Sprintf("%s/%s/%s/%d", owner, repo, req.Kind, req.Number)
	workspacePath := filepath.Join(s.cfg.WorkspaceRoot, owner, repo, workspaceDirectoryName(req.Kind, req.Number))

	if err := os.MkdirAll(filepath.Dir(workspacePath), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create managed workspace root: %w", err)
	}

	if req.Force {
		if err := removeManagedWorkspace(s.cfg.WorkspaceRoot, workspacePath); err != nil {
			return nil, err
		}
	}

	reused, err := workspaceExists(ctx, workspacePath, runGit)
	if err != nil {
		return nil, err
	}
	if !reused {
		if err := cloneWorkspace(ctx, repoURL, workspacePath, s.cfg.BaseRemote, runGit); err != nil {
			return nil, err
		}
	}
	if err := ensureWorkspaceRemote(ctx, workspacePath, repoURL, s.cfg.BaseRemote, runGit); err != nil {
		return nil, err
	}

	if req.Kind == "pr_review" {
		if err := createOrRefreshPRWorkspace(ctx, workspacePath, branch, baseRef, headSHA, s.cfg.BaseRemote, runGit); err != nil {
			return nil, err
		}
	} else if !reused {
		if err := createIssueWorkspace(ctx, workspacePath, branch, baseRef, s.cfg.BaseRemote, runGit); err != nil {
			return nil, err
		}
	}

	return &workspaceResponse{
		Key:          key,
		Kind:         req.Kind,
		Branch:       branch,
		BaseRef:      baseRef,
		WorktreePath: filepath.Clean(workspacePath),
		Reused:       reused,
	}, nil
}

func loadConfig() (config, error) {
	addr := firstNonEmpty(os.Getenv("WORKSPACE_MANAGER_ADDR"), ":4081")

	workspaceRoot := strings.TrimSpace(os.Getenv("WORKSPACE_MANAGER_ROOT"))
	if workspaceRoot == "" {
		homeDir, err := resolveHomeDir()
		if err != nil {
			return config{}, err
		}
		workspaceRoot = filepath.Join(homeDir, ".opencode", "workspaces")
	}

	baseRemote := firstNonEmpty(os.Getenv("WORKSPACE_MANAGER_BASE_REMOTE"), "origin")
	username := firstNonEmpty(os.Getenv("WORKSPACE_MANAGER_USERNAME"), "workspace-manager")

	return config{
		Addr:          addr,
		WorkspaceRoot: filepath.Clean(workspaceRoot),
		BaseRemote:    strings.TrimSpace(baseRemote),
		Username:      username,
		Password:      os.Getenv("WORKSPACE_MANAGER_PASSWORD"),
	}, nil
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

type gitRunner func(ctx context.Context, cwd string, args ...string) (string, error)

func cloneWorkspace(ctx context.Context, repoURL, workspacePath, baseRemote string, run gitRunner) error {
	if _, err := run(ctx, filepath.Dir(workspacePath), "clone", "--origin", baseRemote, repoURL, workspacePath); err != nil {
		return err
	}
	return nil
}

func ensureWorkspaceRemote(ctx context.Context, workspacePath, repoURL, baseRemote string, run gitRunner) error {
	currentRemoteURL, err := run(ctx, workspacePath, "remote", "get-url", baseRemote)
	if err != nil {
		return err
	}
	if strings.TrimSpace(currentRemoteURL) == strings.TrimSpace(repoURL) {
		return nil
	}
	if _, err := run(ctx, workspacePath, "remote", "set-url", baseRemote, repoURL); err != nil {
		return err
	}
	return nil
}

func createIssueWorkspace(ctx context.Context, workspacePath, branch, baseRef, baseRemote string, run gitRunner) error {
	if _, err := run(ctx, workspacePath, "fetch", "--all", "--prune"); err != nil {
		return err
	}

	remoteRef := qualifyRemoteRef(baseRemote, baseRef)
	if err := verifyCommitishWithRunner(ctx, workspacePath, remoteRef, run); err != nil {
		return err
	}
	if _, err := run(ctx, workspacePath, "checkout", "-B", branch, remoteRef); err != nil {
		return err
	}
	return nil
}

func createOrRefreshPRWorkspace(ctx context.Context, workspacePath, branch, baseRef, headSHA, baseRemote string, run gitRunner) error {
	if _, err := run(ctx, workspacePath, "fetch", "--all", "--prune"); err != nil {
		return err
	}

	targetRef := strings.TrimSpace(headSHA)
	if targetRef == "" {
		targetRef = qualifyRemoteRef(baseRemote, baseRef)
	}
	if err := verifyCommitishWithRunner(ctx, workspacePath, targetRef, run); err != nil {
		return err
	}
	if _, err := run(ctx, workspacePath, "reset", "--hard"); err != nil {
		return err
	}
	if _, err := run(ctx, workspacePath, "clean", "-fdx"); err != nil {
		return err
	}
	if _, err := run(ctx, workspacePath, "checkout", "-B", branch, targetRef); err != nil {
		return err
	}
	if _, err := run(ctx, workspacePath, "reset", "--hard", targetRef); err != nil {
		return err
	}
	if _, err := run(ctx, workspacePath, "clean", "-fdx"); err != nil {
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

func verifyCommitishWithRunner(ctx context.Context, cwd, ref string, run gitRunner) error {
	if _, err := run(ctx, cwd, "rev-parse", "--verify", ref+"^{commit}"); err != nil {
		return fmt.Errorf("unable to resolve ref %s to a commit: %w", ref, err)
	}
	return nil
}

func workspaceExists(ctx context.Context, workspacePath string, run gitRunner) (bool, error) {
	info, err := os.Stat(workspacePath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to inspect managed workspace path: %w", err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("managed workspace path is not a directory: %s", workspacePath)
	}

	output, err := run(ctx, workspacePath, "rev-parse", "--show-toplevel")
	if err != nil {
		return false, fmt.Errorf("managed workspace path already exists but is not a git repository: %s", workspacePath)
	}

	resolvedTop := filepath.Clean(strings.TrimSpace(output))
	resolvedWorkspace := filepath.Clean(workspacePath)
	if resolvedTop != resolvedWorkspace {
		return false, fmt.Errorf("managed workspace path resolves to unexpected git root %s (want %s)", resolvedTop, resolvedWorkspace)
	}

	return true, nil
}

func removeManagedWorkspace(managedRoot, targetPath string) error {
	resolvedManagedRoot := filepath.Clean(managedRoot)
	resolvedTargetPath := filepath.Clean(targetPath)
	if !strings.HasPrefix(resolvedTargetPath, resolvedManagedRoot+string(os.PathSeparator)) {
		return fmt.Errorf("refusing to remove unmanaged workspace path: %s", resolvedTargetPath)
	}

	if err := os.RemoveAll(resolvedTargetPath); err != nil {
		return fmt.Errorf("failed to remove managed workspace path %s: %w", resolvedTargetPath, err)
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

func workspaceDirectoryName(kind string, number int) string {
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

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
