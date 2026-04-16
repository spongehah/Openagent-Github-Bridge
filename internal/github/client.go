package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-github/v70/github"
	"github.com/openagent/github-bridge/internal/config"
	"golang.org/x/oauth2"
)

// Client wraps the GitHub API client with convenience methods.
type Client struct {
	client *github.Client
	config config.GitHubConfig
}

// NewClient creates a new GitHub API client.
func NewClient(cfg config.GitHubConfig) *Client {
	var httpClient *http.Client

	if cfg.Token != "" {
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: cfg.Token},
		)
		httpClient = oauth2.NewClient(context.Background(), ts)
	}

	ghClient := github.NewClient(httpClient)

	// Set custom base URL for GitHub Enterprise
	if cfg.APIBaseURL != "" && cfg.APIBaseURL != "https://api.github.com" {
		baseURL := strings.TrimSuffix(cfg.APIBaseURL, "/") + "/"
		ghClient, _ = ghClient.WithEnterpriseURLs(baseURL, baseURL)
	}

	return &Client{
		client: ghClient,
		config: cfg,
	}
}

// CreateComment posts a comment on an issue or pull request.
func (c *Client) CreateComment(ctx context.Context, owner, repo string, number int, body string) error {
	comment := &github.IssueComment{
		Body: github.Ptr(body),
	}

	_, _, err := c.client.Issues.CreateComment(ctx, owner, repo, number, comment)
	if err != nil {
		return fmt.Errorf("failed to create comment: %w", err)
	}

	return nil
}

// GetIssue retrieves an issue by number.
func (c *Client) GetIssue(ctx context.Context, owner, repo string, number int) (*github.Issue, error) {
	issue, _, err := c.client.Issues.Get(ctx, owner, repo, number)
	if err != nil {
		return nil, fmt.Errorf("failed to get issue: %w", err)
	}
	return issue, nil
}

// GetIssueComments retrieves all comments on an issue.
func (c *Client) GetIssueComments(ctx context.Context, owner, repo string, number int) ([]*github.IssueComment, error) {
	opts := &github.IssueListCommentsOptions{
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	var allComments []*github.IssueComment
	for {
		comments, resp, err := c.client.Issues.ListComments(ctx, owner, repo, number, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list comments: %w", err)
		}
		allComments = append(allComments, comments...)

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return allComments, nil
}

// GetPullRequest retrieves a pull request by number.
func (c *Client) GetPullRequest(ctx context.Context, owner, repo string, number int) (*github.PullRequest, error) {
	pr, _, err := c.client.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		return nil, fmt.Errorf("failed to get pull request: %w", err)
	}
	return pr, nil
}

// GetPullRequestFiles retrieves the files changed in a pull request.
func (c *Client) GetPullRequestFiles(ctx context.Context, owner, repo string, number int) ([]*github.CommitFile, error) {
	opts := &github.ListOptions{
		PerPage: 100,
	}

	var allFiles []*github.CommitFile
	for {
		files, resp, err := c.client.PullRequests.ListFiles(ctx, owner, repo, number, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list PR files: %w", err)
		}
		allFiles = append(allFiles, files...)

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return allFiles, nil
}

// CreatePullRequestReview creates a review on a pull request.
func (c *Client) CreatePullRequestReview(ctx context.Context, owner, repo string, number int, body string, event string) error {
	review := &github.PullRequestReviewRequest{
		Body:  github.Ptr(body),
		Event: github.Ptr(event), // "APPROVE", "REQUEST_CHANGES", "COMMENT"
	}

	_, _, err := c.client.PullRequests.CreateReview(ctx, owner, repo, number, review)
	if err != nil {
		return fmt.Errorf("failed to create PR review: %w", err)
	}

	return nil
}

// AddLabels adds labels to an issue or pull request.
func (c *Client) AddLabels(ctx context.Context, owner, repo string, number int, labels []string) error {
	_, _, err := c.client.Issues.AddLabelsToIssue(ctx, owner, repo, number, labels)
	if err != nil {
		return fmt.Errorf("failed to add labels: %w", err)
	}
	return nil
}

// CreateReaction adds a reaction to a comment.
func (c *Client) CreateReaction(ctx context.Context, owner, repo string, commentID int64, reaction string) error {
	_, _, err := c.client.Reactions.CreateIssueCommentReaction(ctx, owner, repo, commentID, reaction)
	if err != nil {
		return fmt.Errorf("failed to create reaction: %w", err)
	}
	return nil
}

// GetFileContent retrieves the content of a file from the repository.
func (c *Client) GetFileContent(ctx context.Context, owner, repo, path, ref string) (string, error) {
	opts := &github.RepositoryContentGetOptions{
		Ref: ref,
	}

	fileContent, _, _, err := c.client.Repositories.GetContents(ctx, owner, repo, path, opts)
	if err != nil {
		return "", fmt.Errorf("failed to get file content: %w", err)
	}

	content, err := fileContent.GetContent()
	if err != nil {
		return "", fmt.Errorf("failed to decode file content: %w", err)
	}

	return content, nil
}

// CreateBranch creates a new branch from a reference.
func (c *Client) CreateBranch(ctx context.Context, owner, repo, branchName, fromRef string) error {
	// Get the reference SHA
	ref, _, err := c.client.Git.GetRef(ctx, owner, repo, "refs/heads/"+fromRef)
	if err != nil {
		return fmt.Errorf("failed to get reference: %w", err)
	}

	// Create new branch
	newRef := &github.Reference{
		Ref:    github.Ptr("refs/heads/" + branchName),
		Object: ref.Object,
	}

	_, _, err = c.client.Git.CreateRef(ctx, owner, repo, newRef)
	if err != nil {
		return fmt.Errorf("failed to create branch: %w", err)
	}

	return nil
}

// CreatePullRequest creates a new pull request.
func (c *Client) CreatePullRequest(ctx context.Context, owner, repo, title, body, head, base string) (*github.PullRequest, error) {
	newPR := &github.NewPullRequest{
		Title: github.Ptr(title),
		Body:  github.Ptr(body),
		Head:  github.Ptr(head),
		Base:  github.Ptr(base),
	}

	pr, _, err := c.client.PullRequests.Create(ctx, owner, repo, newPR)
	if err != nil {
		return nil, fmt.Errorf("failed to create pull request: %w", err)
	}

	return pr, nil
}

// GetRepository retrieves repository information.
func (c *Client) GetRepository(ctx context.Context, owner, repo string) (*github.Repository, error) {
	repository, _, err := c.client.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get repository: %w", err)
	}
	return repository, nil
}

// GetRawClient returns the underlying go-github client for advanced operations.
func (c *Client) GetRawClient() *github.Client {
	return c.client
}
