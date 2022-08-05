package githubprovider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-github/v30/github"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
	"moul.io/depviz/v3/internal/dvmodel"
	"moul.io/multipmuri"
)

type Opts struct {
	Since  *time.Time  `json:"since"`
	Logger *zap.Logger `json:"-"`
}

func getGitHubClient(ctx context.Context, gitHubToken string) *github.Client {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: gitHubToken})
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	return client
}

func FetchRepo(ctx context.Context, entity multipmuri.Entity, token string, out chan<- dvmodel.Batch, opts Opts) { // nolint:interfacer
	if opts.Logger == nil {
		opts.Logger = zap.NewNop()
	}

	type multipmuriMinimalInterface interface {
		Repo() *multipmuri.GitHubRepo
	}
	target, ok := entity.(multipmuriMinimalInterface)
	if !ok {
		opts.Logger.Warn("invalid entity", zap.String("entity", fmt.Sprintf("%v", entity.String())))
		return
	}
	repo := target.Repo()

	// create client
	client := getGitHubClient(ctx, token)

	// queries
	totalIssues := 0
	callOpts := &github.IssueListByRepoOptions{State: "all"}
	if opts.Since != nil {
		callOpts.Since = *opts.Since
	}
	for {
		issues, resp, err := client.Issues.ListByRepo(ctx, repo.OwnerID(), repo.RepoID(), callOpts)
		if err != nil {
			opts.Logger.Error("fetch GitHub issues", zap.Error(err))
			return
		}
		totalIssues += len(issues)
		opts.Logger.Debug("paginate",
			zap.Any("opts", opts),
			zap.String("provider", "github"),
			zap.String("repo", repo.String()),
			zap.Int("new-issues", len(issues)),
			zap.Int("total-issues", totalIssues),
		)

		if len(issues) > 0 {
			batch := fromIssues(issues, opts.Logger)
			out <- batch
		}

		// handle pagination
		if resp.NextPage == 0 {
			break
		}
		callOpts.Page = resp.NextPage
	}

	if rateLimits, _, err := client.RateLimits(ctx); err == nil {
		opts.Logger.Debug("github API rate limiting", zap.Stringer("limit", rateLimits.GetCore()))
	}

	// FIXME: fetch incomplete/old users, orgs, teams & repos
}

func AddAssignee(ctx context.Context, assignee string, id int, owner string, repo string, gitHubToken string, logger *zap.Logger) bool {
	client := getGitHubClient(ctx, gitHubToken)

	if assignee == "" {
		logger.Info("remove assignee", zap.Int("id", id), zap.String("owner", owner), zap.String("repo", repo))
		return false
	}
	_, resp, err := client.Issues.AddAssignees(ctx, owner, repo, id, []string{assignee})
	if err != nil {
		logger.Error("add assignee", zap.Error(err))
		return false
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		logger.Info("add assignee", zap.Int("status code", resp.StatusCode))
		return true
	}
	logger.Warn("add assignee", zap.String("assignee", assignee), zap.Int("id", id), zap.String("owner", owner), zap.String("repo", repo), zap.Int("status", resp.StatusCode))
	return false
}

func SubscribeToRepo(ctx context.Context, owner string, repo string, current bool, gitHubToken string, logger *zap.Logger) bool {
	client := getGitHubClient(ctx, gitHubToken)

	// FIXME: should be replaced by
	// client.Activity.SetRepositorySubscription(ctx, owner, repo, &github.Subscription{Subscribed: github.Bool(!current)})
	// but this seems to be broken now
	if current {
		_, err := client.Activity.DeleteRepositorySubscription(ctx, owner, repo)
		if err != nil {
			logger.Error("unsubscribe from repo", zap.Error(err))
			return false
		}
	} else {
		_, _, err := client.Activity.SetRepositorySubscription(ctx, owner, repo, &github.Subscription{Subscribed: github.Bool(true)})
		if err != nil {
			logger.Error("subscribe to repo", zap.Error(err))
			return false
		}
	}
	return true
}

func IssueAddMetadata(ctx context.Context, id int, owner string, repo string, gitHubToken string, metadata string, logger *zap.Logger) bool {
	client := getGitHubClient(ctx, gitHubToken)

	issue, _, err := client.Issues.Get(ctx, owner, repo, id)
	if err != nil {
		logger.Error("get issue", zap.Error(err))
		return false
	}
	if err != nil {
		logger.Error("get issue", zap.Error(err))
		return false
	}

	metadata = strings.Replace(metadata, "|", "\n", -1)

	var newBody string
	// add metadata at the end of the body in the "-- depviz auto --" section
	if issue.Body != nil {
		newBody = *issue.Body

		// check if the section exist(mark to change)
		if !strings.Contains(*issue.Body, "-- depviz auto --") {
			newBody += "\n\n-- depviz auto --"
		}

		// return true if duplicate
		//TODO

		newBody += "\n" + metadata
	} else {
		newBody = "\n\n\n-- depviz auto --\n" + metadata
	}
	_, resp, err := client.Issues.Edit(ctx, owner, repo, id, &github.IssueRequest{Body: &newBody})
	if err != nil {
		logger.Error("add metadata", zap.Error(err))
		return false
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		logger.Info("add metadata", zap.Int("status code", resp.StatusCode))
		return true
	}
	logger.Warn("add metadata", zap.String("metadata", metadata), zap.Int("id", id), zap.String("owner", owner), zap.String("repo", repo), zap.Int("status", resp.StatusCode))
	return false
}

func IssueAddComment(ctx context.Context, id int, owner string, repo string, gitHubToken string, comment string, logger *zap.Logger) bool {
	client := getGitHubClient(ctx, gitHubToken)

	_, resp, err := client.Issues.CreateComment(ctx, owner, repo, id, &github.IssueComment{Body: &comment})
	if err != nil {
		logger.Error("add comment", zap.Error(err))
		return false
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		logger.Info("add comment", zap.Int("status code", resp.StatusCode))
		return true
	}
	logger.Warn("add comment", zap.String("comment", comment), zap.Int("id", id), zap.String("owner", owner), zap.String("repo", repo), zap.Int("status", resp.StatusCode))
	return false
}
