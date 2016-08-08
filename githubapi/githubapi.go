// Package githubapi implements notifications.Service using GitHub API client.
package githubapi

import (
	"fmt"
	"html/template"
	"log"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/go-github/github"
	"github.com/shurcooL/notifications"
	"github.com/shurcooL/users"
	"golang.org/x/net/context"
)

// NewService creates a GitHub-backed notifications.Service using given GitHub clients.
// At this time it infers the current user from the client (its authentication info), and cannot be used to serve multiple users.
//
// Caching can't be used for Activity.ListNotifications until GitHub API fixes the
// odd behavior of returning 304 even when some notifications get marked as read.
// Otherwise read notifications remain forever (until a new notification comes in).
// That's why we need clientNoCache.
func NewService(clientNoCache *github.Client, client *github.Client) notifications.Service {
	return service{
		clNoCache: clientNoCache,
		cl:        client,
	}
}

type service struct {
	clNoCache *github.Client // TODO: Start using cache whenever possible, remove this.
	cl        *github.Client
}

func (s service) List(ctx context.Context, opt interface{}) (notifications.Notifications, error) {
	var ns []notifications.Notification

	ghNotifications, _, err := s.clNoCache.Activity.ListNotifications(nil)
	if err != nil {
		return nil, err
	}
	for _, n := range ghNotifications {
		notification := notifications.Notification{
			RepoSpec:  notifications.RepoSpec{URI: "github.com/" + *n.Repository.FullName},
			RepoURL:   template.URL("https://github.com/" + *n.Repository.FullName),
			Title:     *n.Subject.Title,
			UpdatedAt: *n.UpdatedAt,
		}

		switch *n.Subject.Type {
		case "Issue":
			switch state, err := s.getIssueState(*n.Subject.URL); {
			case err == nil && state == "open":
				notification.Icon = "issue-opened"
				notification.Color = notifications.RGB{R: 0x6c, G: 0xc6, B: 0x44}
			case err == nil && state == "closed":
				notification.Icon = "issue-closed"
				notification.Color = notifications.RGB{R: 0xbd, G: 0x2c, B: 0x00}
			default:
				notification.Icon = "issue-opened"
			}
			notification.HTMLURL, err = getIssueURL(*n.Subject)
			if err != nil {
				return ns, err
			}
		case "PullRequest":
			notification.Icon = "git-pull-request"
			switch state, err := s.getPullRequestState(*n.Subject.URL); {
			case err == nil && state == "open":
				notification.Color = notifications.RGB{R: 0x6c, G: 0xc6, B: 0x44}
			case err == nil && state == "closed":
				notification.Color = notifications.RGB{R: 0xbd, G: 0x2c, B: 0x00}
			case err == nil && state == "merged":
				notification.Color = notifications.RGB{R: 0x6e, G: 0x54, B: 0x94}
			}
			notification.HTMLURL, err = getPullRequestURL(*n.Subject)
			if err != nil {
				return ns, err
			}
		case "Commit":
			notification.Icon = "git-commit"
			notification.Color = notifications.RGB{R: 0x76, G: 0x76, B: 0x76}
			notification.HTMLURL, err = getCommitURL(*n.Subject)
			if err != nil {
				return ns, err
			}
		case "Release":
			notification.Icon = "tag"
			notification.Color = notifications.RGB{R: 0x76, G: 0x76, B: 0x76}
			notification.HTMLURL, err = getReleaseURL(*n.Subject)
			if err != nil {
				return ns, err
			}
		default:
			log.Printf("unsupported *n.Subject.Type: %q\n", *n.Subject.Type)
		}

		ns = append(ns, notification)
	}

	return ns, nil
}

func (s service) Count(ctx context.Context, opt interface{}) (uint64, error) {
	ghNotifications, _, err := s.clNoCache.Activity.ListNotifications(nil)
	return uint64(len(ghNotifications)), err
}

func (s service) MarkRead(ctx context.Context, appID string, rs notifications.RepoSpec, threadID uint64) error {
	repo := ghRepoSpec(rs)
	ns, _, err := s.cl.Activity.ListRepositoryNotifications(repo.Owner, repo.Repo, nil)
	if err != nil {
		return fmt.Errorf("failed to ListRepositoryNotifications: %v", err)
	}
	for _, n := range ns {
		if *n.Subject.Type != appID {
			continue
		}
		_, issueID, err := parseIssueSpec(*n.Subject.URL)
		if err != nil {
			return fmt.Errorf("failed to parseIssueSpec: %v", err)
		}
		if uint64(issueID) != threadID {
			continue
		}

		_, err = s.cl.Activity.MarkThreadRead(*n.ID)
		if err != nil {
			return fmt.Errorf("failed to MarkThreadRead: %v", err)
		}
		break
	}
	return nil
}

func (s service) Notify(ctx context.Context, appID string, repo notifications.RepoSpec, threadID uint64, op notifications.NotificationRequest) error {
	// Nothing to do. GitHub takes care of this on their end, even when creating comments/issues via API.
	return nil
}

func (s service) Subscribe(ctx context.Context, appID string, repo notifications.RepoSpec, threadID uint64, subscribers []users.UserSpec) error {
	// Nothing to do. GitHub takes care of this on their end, even when creating comments/issues via API.
	return nil
}

func (s service) getIssueState(issueAPIURL string) (string, error) {
	req, err := s.cl.NewRequest("GET", issueAPIURL, nil)
	if err != nil {
		return "", err
	}
	issue := new(github.Issue)
	_, err = s.cl.Do(req, issue)
	if err != nil {
		return "", err
	}
	if issue.State == nil {
		return "", fmt.Errorf("for some reason issue.State is nil for %q: %v", issueAPIURL, issue)
	}
	return *issue.State, nil
}

func (s service) getPullRequestState(prAPIURL string) (string, error) {
	req, err := s.cl.NewRequest("GET", prAPIURL, nil)
	if err != nil {
		return "", err
	}
	pr := new(github.PullRequest)
	_, err = s.cl.Do(req, pr)
	if err != nil {
		return "", err
	}
	if pr.State == nil || pr.Merged == nil {
		return "", fmt.Errorf("for some reason pr.State or pr.Merged is nil for %q: %v", prAPIURL, pr)
	}
	switch {
	default:
		return *pr.State, nil
	case *pr.Merged:
		return "merged", nil
	}
}

func getIssueURL(n github.NotificationSubject) (template.URL, error) {
	rs, issueID, err := parseIssueSpec(*n.URL)
	if err != nil {
		return "", err
	}
	var fragment string
	if _, commentID, err := parseIssueCommentSpec(*n.LatestCommentURL); err == nil {
		fragment = fmt.Sprintf("#comment-%d", commentID)
	}
	return template.URL(fmt.Sprintf("https://github.com/%s/issues/%d%s", rs.URI, issueID, fragment)), nil
}

func getPullRequestURL(n github.NotificationSubject) (template.URL, error) {
	rs, prID, err := parsePullRequestSpec(*n.URL)
	if err != nil {
		return "", err
	}
	var fragment string
	if _, commentID, err := parseIssueCommentSpec(*n.LatestCommentURL); err == nil {
		fragment = fmt.Sprintf("#comment-%d", commentID)
	}
	return template.URL(fmt.Sprintf("https://github.com/%s/pull/%d%s", rs.URI, prID, fragment)), nil
}

func getCommitURL(n github.NotificationSubject) (template.URL, error) {
	rs, commit, err := parseSpec(*n.URL, "commits")
	if err != nil {
		return "", err
	}
	return template.URL(fmt.Sprintf("https://github.com/%s/commit/%s", rs.URI, commit)), nil
}

func getReleaseURL(n github.NotificationSubject) (template.URL, error) {
	rs, _, err := parseSpec(*n.URL, "releases")
	if err != nil {
		return "", err
	}
	return template.URL(fmt.Sprintf("https://github.com/%s/releases/tag/%s", rs.URI, *n.Title)), nil
}

func parseIssueSpec(issueAPIURL string) (_ notifications.RepoSpec, issueID int, _ error) {
	rs, id, err := parseSpec(issueAPIURL, "issues")
	if err != nil {
		return notifications.RepoSpec{}, 0, err
	}
	issueID, err = strconv.Atoi(id)
	if err != nil {
		return notifications.RepoSpec{}, 0, err
	}
	return rs, issueID, nil
}

func parsePullRequestSpec(prAPIURL string) (_ notifications.RepoSpec, prID int, _ error) {
	rs, id, err := parseSpec(prAPIURL, "pulls")
	if err != nil {
		return notifications.RepoSpec{}, 0, err
	}
	prID, err = strconv.Atoi(id)
	if err != nil {
		return notifications.RepoSpec{}, 0, err
	}
	return rs, prID, nil
}

func parseSpec(apiURL, specType string) (_ notifications.RepoSpec, id string, _ error) {
	u, err := url.Parse(apiURL)
	if err != nil {
		return notifications.RepoSpec{}, "", err
	}
	e := strings.Split(u.Path, "/")
	if len(e) < 5 {
		return notifications.RepoSpec{}, "", fmt.Errorf("unexpected path (too few elements): %q", u.Path)
	}
	if got, want := e[len(e)-2], specType; got != want {
		return notifications.RepoSpec{}, "", fmt.Errorf("unexpected path element %q, expecting %q", got, want)
	}
	return notifications.RepoSpec{URI: e[len(e)-4] + "/" + e[len(e)-3]}, e[len(e)-1], nil
}

func parseIssueCommentSpec(issueAPIURL string) (notifications.RepoSpec, int, error) {
	u, err := url.Parse(issueAPIURL)
	if err != nil {
		return notifications.RepoSpec{}, 0, err
	}
	e := strings.Split(u.Path, "/")
	if len(e) < 6 {
		return notifications.RepoSpec{}, 0, fmt.Errorf("unexpected path (too few elements): %q", u.Path)
	}
	if got, want := e[len(e)-2], "comments"; got != want {
		return notifications.RepoSpec{}, 0, fmt.Errorf("unexpected path element %q, expecting %q", got, want)
	}
	if got, want := e[len(e)-3], "issues"; got != want {
		return notifications.RepoSpec{}, 0, fmt.Errorf("unexpected path element %q, expecting %q", got, want)
	}
	id, err := strconv.Atoi(e[len(e)-1])
	if err != nil {
		return notifications.RepoSpec{}, 0, err
	}
	return notifications.RepoSpec{URI: e[len(e)-5] + "/" + e[len(e)-4]}, id, nil
}

type repoSpec struct {
	Owner string
	Repo  string
}

func ghRepoSpec(repo notifications.RepoSpec) repoSpec {
	ownerRepo := strings.Split(repo.URI, "/")
	if len(ownerRepo) != 2 {
		panic(fmt.Errorf(`RepoSpec is not of form "owner/repo": %v`, repo))
	}
	return repoSpec{
		Owner: ownerRepo[0],
		Repo:  ownerRepo[1],
	}
}

func ghUser(user *github.User) users.User {
	return users.User{
		UserSpec: users.UserSpec{
			ID:     uint64(*user.ID),
			Domain: "github.com",
		},
		Login:     *user.Login,
		AvatarURL: template.URL(*user.AvatarURL),
		HTMLURL:   template.URL(*user.HTMLURL),
	}
}
