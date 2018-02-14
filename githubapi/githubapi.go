// Package githubapi implements notifications.Service using GitHub API clients.
package githubapi

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/github"
	"github.com/google/go-querystring/query"
	"github.com/gregjones/httpcache"
	"github.com/shurcooL/githubql"
	"github.com/shurcooL/notifications"
	"github.com/shurcooL/users"
)

// NewService creates a GitHub-backed notifications.Service using given GitHub clients.
// At this time it infers the current user from the client (its authentication info),
// and cannot be used to serve multiple users.
//
// Caching can't be used for Activity.ListNotifications until GitHub REST API v3 fixes the
// odd behavior of returning 304 even when some notifications get marked as read.
// Otherwise read notifications remain forever (until a new notification comes in).
//
// This service uses Cache-Control: no-cache request header to disable caching.
// Responses from cache must be marked with "X-From-Cache" header (i.e., the field
// MarkCachedResponses in httpcache.Transport must be set to true).
//
// If router is nil, GitHubRouter is used, which links to https://github.com.
func NewService(clientV3 *github.Client, clientV4 *githubql.Client, router Router) notifications.Service {
	if router == nil {
		router = GitHubRouter{}
	}
	return service{
		clV3: clientV3,
		clV4: clientV4,
		r:    router,
	}
}

// Router provides HTML URLs of GitHub notification subjects.
type Router interface {
	// IssueURL returns the HTML URL of the specified GitHub issue.
	IssueURL(owner, repo string, issueID, commentID uint64) string

	// PullRequestURL returns the HTML URL of the specified GitHub pull request.
	PullRequestURL(owner, repo string, prID, commentID uint64) string
}

type service struct {
	clV3 *github.Client   // GitHub REST API v3 client.
	clV4 *githubql.Client // GitHub GraphQL API v4 client.
	r    Router
}

func (s service) List(ctx context.Context, opt notifications.ListOptions) (notifications.Notifications, error) {
	var ns []notifications.Notification

	ghOpt := &github.NotificationListOptions{
		All:         opt.All,
		ListOptions: github.ListOptions{PerPage: 100},
	}
	var ghNotifications []*github.Notification
	switch opt.Repo {
	case nil:
		for {
			ns, resp, err := ghListNotifications(ctx, s.clV3, ghOpt, false)
			if err != nil {
				return nil, err
			}
			ghNotifications = append(ghNotifications, ns...)
			if resp.NextPage == 0 {
				break
			}
			ghOpt.Page = resp.NextPage
		}
	default:
		repo, err := ghRepoSpec(*opt.Repo)
		if err != nil {
			return nil, err
		}
		for {
			ns, resp, err := ghListRepositoryNotifications(ctx, s.clV3, repo.Owner, repo.Repo, ghOpt, false)
			if err != nil {
				return nil, err
			}
			ghNotifications = append(ghNotifications, ns...)
			if resp.NextPage == 0 {
				break
			}
			ghOpt.Page = resp.NextPage
		}
	}
	for _, n := range ghNotifications {
		notification := notifications.Notification{
			RepoSpec:   notifications.RepoSpec{URI: "github.com/" + *n.Repository.FullName},
			ThreadType: *n.Subject.Type,
			Title:      *n.Subject.Title,
			UpdatedAt:  *n.UpdatedAt,
			Read:       !*n.Unread,

			Participating: *n.Reason != "subscribed", // According to https://developer.github.com/v3/activity/notifications/#notification-reasons, "subscribed" reason means "you're watching the repository", and all other reasons imply participation.
			Mentioned:     *n.Reason == "mention",
		}

		// TODO: We're inside range ghNotifications loop here, and doing a single
		//       GraphQL query for each Issue/PR. It would be better to combine
		//       all the individual queries into a single GraphQL query and execute
		//       that in one request instead. Need to come up with a good way of making
		//       this possible. See https://github.com/shurcooL/githubql/issues/17.

		switch *n.Subject.Type {
		case "Issue":
			// This makes a single GraphQL API call. It's relatively slow/expensive
			// because it happens in the ghNotifications loop.

			rs, issueID, err := parseIssueSpec(*n.Subject.URL)
			if err != nil {
				return ns, err
			}
			notification.ThreadID = issueID
			var q struct {
				Repository struct {
					Issue struct {
						State    githubql.IssueState
						Author   *githubqlActor
						Comments struct {
							Nodes []struct {
								Author     *githubqlActor
								DatabaseID uint64
							}
						} `graphql:"comments(last:1)"`
					} `graphql:"issue(number:$issueNumber)"`
				} `graphql:"repository(owner:$repositoryOwner,name:$repositoryName)"`
			}
			variables := map[string]interface{}{
				"repositoryOwner": githubql.String(rs.Owner),
				"repositoryName":  githubql.String(rs.Repo),
				"issueNumber":     githubql.Int(issueID),
			}
			err = s.clV4.Query(ctx, &q, variables)
			if err != nil {
				return ns, err
			}
			switch q.Repository.Issue.State {
			case githubql.IssueStateOpen:
				notification.Icon = "issue-opened"
				notification.Color = notifications.RGB{R: 0x6c, G: 0xc6, B: 0x44} // Green.
			case githubql.IssueStateClosed:
				notification.Icon = "issue-closed"
				notification.Color = notifications.RGB{R: 0xbd, G: 0x2c, B: 0x00} // Red.
			}
			switch len(q.Repository.Issue.Comments.Nodes) {
			case 0:
				notification.Actor = ghActor(q.Repository.Issue.Author)
				notification.HTMLURL = s.r.IssueURL(rs.Owner, rs.Repo, issueID, 0)
			case 1:
				notification.Actor = ghActor(q.Repository.Issue.Comments.Nodes[0].Author)
				notification.HTMLURL = s.r.IssueURL(rs.Owner, rs.Repo, issueID, q.Repository.Issue.Comments.Nodes[0].DatabaseID)
			}
		case "PullRequest":
			// This makes a single GraphQL API call. It's relatively slow/expensive
			// because it happens in the ghNotifications loop.

			rs, prID, err := parsePullRequestSpec(*n.Subject.URL)
			if err != nil {
				return ns, err
			}
			notification.ThreadID = prID
			var q struct {
				Repository struct {
					PullRequest struct {
						State    githubql.PullRequestState
						Author   *githubqlActor
						Comments struct {
							Nodes []struct {
								Author     *githubqlActor
								DatabaseID uint64
							}
						} `graphql:"comments(last:1)"`
					} `graphql:"pullRequest(number:$prNumber)"`
				} `graphql:"repository(owner:$repositoryOwner,name:$repositoryName)"`
			}
			variables := map[string]interface{}{
				"repositoryOwner": githubql.String(rs.Owner),
				"repositoryName":  githubql.String(rs.Repo),
				"prNumber":        githubql.Int(prID),
			}
			err = s.clV4.Query(ctx, &q, variables)
			if err != nil {
				return ns, err
			}
			notification.Icon = "git-pull-request"
			switch q.Repository.PullRequest.State {
			case githubql.PullRequestStateOpen:
				notification.Color = notifications.RGB{R: 0x6c, G: 0xc6, B: 0x44} // Green.
			case githubql.PullRequestStateClosed:
				notification.Color = notifications.RGB{R: 0xbd, G: 0x2c, B: 0x00} // Red.
			case githubql.PullRequestStateMerged:
				notification.Color = notifications.RGB{R: 0x6e, G: 0x54, B: 0x94} // Purple.
			}
			switch len(q.Repository.PullRequest.Comments.Nodes) {
			case 0:
				notification.Actor = ghActor(q.Repository.PullRequest.Author)
				notification.HTMLURL = s.r.PullRequestURL(rs.Owner, rs.Repo, prID, 0)
			case 1:
				notification.Actor = ghActor(q.Repository.PullRequest.Comments.Nodes[0].Author)
				notification.HTMLURL = s.r.PullRequestURL(rs.Owner, rs.Repo, prID, q.Repository.PullRequest.Comments.Nodes[0].DatabaseID)
			}
		case "Commit":
			// getNotificationActor makes a single API call. It's relatively slow/expensive
			// because it happens in the ghNotifications loop.
			// TODO: Fetch using GraphQL.

			id, err := strconv.ParseUint(*n.ID, 10, 64)
			if err != nil {
				return ns, fmt.Errorf("notifications/githubapi: failed to parse Commit notification ID %q to uint64: %v", *n.ID, err)
			}
			notification.ThreadID = id
			notification.Icon = "git-commit"
			notification.Color = notifications.RGB{R: 0x76, G: 0x76, B: 0x76} // Gray.
			notification.Actor, err = s.getNotificationActor(ctx, *n.Subject)
			if err != nil {
				return ns, err
			}
			notification.HTMLURL, err = getCommitURL(*n.Subject)
			if err != nil {
				return ns, err
			}
		case "Release":
			// getNotificationActor and getReleaseURL make two API calls. It's relatively slow/expensive
			// because it happens in the ghNotifications loop.
			// TODO: Fetch using GraphQL.

			id, err := strconv.ParseUint(*n.ID, 10, 64)
			if err != nil {
				return ns, fmt.Errorf("notifications/githubapi: failed to parse Release notification ID %q to uint64: %v", *n.ID, err)
			}
			notification.ThreadID = id
			notification.Icon = "tag"
			notification.Color = notifications.RGB{R: 0x76, G: 0x76, B: 0x76} // Gray.
			notification.Actor, err = s.getNotificationActor(ctx, *n.Subject)
			if err != nil {
				return ns, err
			}
			notification.HTMLURL, err = s.getReleaseURL(ctx, *n.Subject.URL)
			if err != nil {
				return ns, err
			}
		case "RepositoryInvitation":
			// getNotificationActor makes a single API call. It's relatively slow/expensive
			// because it happens in the ghNotifications loop.
			// TODO: Fetch using GraphQL.

			id, err := strconv.ParseUint(*n.ID, 10, 64)
			if err != nil {
				return ns, fmt.Errorf("notifications/githubapi: failed to parse RepositoryInvitation notification ID %q to uint64: %v", *n.ID, err)
			}
			notification.ThreadID = id
			notification.Icon = "mail"
			notification.Color = notifications.RGB{R: 0x76, G: 0x76, B: 0x76} // Gray.
			notification.Actor, err = s.getNotificationActor(ctx, *n.Subject)
			if err != nil {
				return ns, err
			}
			notification.HTMLURL = getRepositoryInvitationURL(*n.Repository.FullName)
		default:
			log.Printf("unsupported *n.Subject.Type: %q\n", *n.Subject.Type)
		}

		ns = append(ns, notification)
	}

	return ns, nil
}

func (s service) Count(ctx context.Context, opt interface{}) (uint64, error) {
	ghOpt := &github.NotificationListOptions{ListOptions: github.ListOptions{PerPage: 1}}
	ghNotifications, resp, err := ghListNotifications(ctx, s.clV3, ghOpt, false)
	if err != nil {
		return 0, err
	}
	if resp.LastPage != 0 {
		return uint64(resp.LastPage), nil
	} else {
		return uint64(len(ghNotifications)), nil
	}
}

func (s service) MarkRead(ctx context.Context, rs notifications.RepoSpec, threadType string, threadID uint64) error {
	switch threadType {
	case "Commit", "Release", "RepositoryInvitation":
		_, err := s.clV3.Activity.MarkThreadRead(ctx, strconv.FormatUint(threadID, 10))
		if err != nil {
			return fmt.Errorf("MarkRead: failed to MarkThreadRead: %v", err)
		}
		return nil
	case "Issue", "PullRequest":
		// For these thread types, thread ID is not the notification ID, but rather the
		// issue/PR number. We need to find a matching notification, if any exists.
		// Handled below.

		// Note: If we can always parse the notification ID (a numeric string like "230400425")
		//       from GitHub into a uint64 reliably, then we can skip the whole list repo notifications
		//       and match stuff dance, and just do Activity.MarkThreadRead(ctx, threadID) directly...
		// Update: Not quite. We need to return actual issue IDs as ThreadIDs in List, so that
		//         issuesapp.augmentUnread works correctly. But maybe if we can store it in another
		//         field...
	default:
		return fmt.Errorf("MarkRead: unsupported threadType: %v", threadType)
	}

	repo, err := ghRepoSpec(rs)
	if err != nil {
		return err
	}
	// It's okay to use with-cache client here, because we don't mind seeing read notifications
	// for the purposes of MarkRead. They'll be skipped if the notification ID doesn't match.
	cached, resp, err := ghListRepositoryNotifications(ctx, s.clV3, repo.Owner, repo.Repo, nil, true)
	if err != nil {
		return fmt.Errorf("failed to ListRepositoryNotifications: %v", err)
	}
	n, err := findNotification(cached, threadType, threadID)
	if err != nil {
		return err
	}
	// However, there are sometimes caching issues causing stale repository notifications
	// to be retrieved from cache, and a legitimate existing notification is not marked read.
	// So fall back to skipping cache, if we can't find a notification and the response
	// we got was from cache (rather than origin server).
	if _, fromCache := resp.Response.Header[httpcache.XFromCache]; n == nil && fromCache {
		uncached, _, err := ghListRepositoryNotifications(ctx, s.clV3, repo.Owner, repo.Repo, nil, false)
		if err != nil {
			return fmt.Errorf("failed to ListRepositoryNotifications: %v", err)
		}
		n, err = findNotification(uncached, threadType, threadID)
		if err != nil {
			return err
		}

		if n != nil {
			log.Printf(`MarkRead: did not find notification %s/%s %s %d within cached
%d notifications:
%s
but did find within uncached
%d notifications:
%s
`, repo.Owner, repo.Repo, threadType, threadID, len(cached), notificationsString(cached), len(uncached), notificationsString(uncached))
		}
	}
	switch n {
	default:
		// Found a matching notification, mark it read.
		_, err = s.clV3.Activity.MarkThreadRead(ctx, *n.ID)
		if err != nil {
			return fmt.Errorf("MarkRead: failed to MarkThreadRead: %v", err)
		}
		return nil
	case nil:
		// Didn't find any matching notification to mark read.
		// Nothing to do.
		return nil
	}
}

// findNotification tries to find a notification that matches
// the provided threadType and threadID.
// threadType must be one of "Issue" or "PullRequest".
// It returns nil if no matching notification is found, and
// any error encountered.
func findNotification(ns []*github.Notification, threadType string, threadID uint64) (*github.Notification, error) {
	for _, n := range ns {
		if *n.Subject.Type != threadType {
			// Mismatched thread type.
			continue
		}

		var id uint64
		switch *n.Subject.Type {
		case "Issue":
			var err error
			_, id, err = parseIssueSpec(*n.Subject.URL)
			if err != nil {
				return nil, fmt.Errorf("failed to parseIssueSpec: %v", err)
			}
		case "PullRequest":
			var err error
			_, id, err = parsePullRequestSpec(*n.Subject.URL)
			if err != nil {
				return nil, fmt.Errorf("failed to parsePullRequestSpec: %v", err)
			}
		}
		if id != threadID {
			// Mismatched thread ID.
			continue
		}

		// Found a matching notification.
		return n, nil
	}
	return nil, nil
}

// notificationsString returns a string representation of notifications ns.
func notificationsString(ns []*github.Notification) string {
	var ss []string
	for _, n := range ns {
		ss = append(ss, "\t"+strings.TrimPrefix(*n.Subject.URL, "https://api.github.com/"))
	}
	return strings.Join(ss, "\n")
}

func (s service) MarkAllRead(ctx context.Context, rs notifications.RepoSpec) error {
	repo, err := ghRepoSpec(rs)
	if err != nil {
		return err
	}
	_, err = s.clV3.Activity.MarkRepositoryNotificationsRead(ctx, repo.Owner, repo.Repo, time.Now())
	if err != nil {
		return fmt.Errorf("MarkAllRead: failed to MarkRepositoryNotificationsRead: %v", err)
	}
	return nil
}

func (s service) Notify(ctx context.Context, repo notifications.RepoSpec, threadType string, threadID uint64, op notifications.NotificationRequest) error {
	// Nothing to do. GitHub takes care of this on their end, even when creating comments/issues via API.
	return nil
}

func (s service) Subscribe(ctx context.Context, repo notifications.RepoSpec, threadType string, threadID uint64, subscribers []users.UserSpec) error {
	// Nothing to do. GitHub takes care of this on their end, even when creating comments/issues via API.
	return nil
}

// getNotificationActor tries to follow the LatestCommentURL, if not-nil,
// to fetch an object that contains a User or Author, who is taken to be
// the actor that triggered the notification. It returns an error only if
// something unexpected happened.
func (s service) getNotificationActor(ctx context.Context, subject github.NotificationSubject) (users.User, error) {
	var apiURL string
	if subject.LatestCommentURL != nil {
		apiURL = *subject.LatestCommentURL
	} else if subject.URL != nil {
		// URL is used as fallback, if LatestCommentURL isn't present. It can happen for inline comments on PRs, etc.
		apiURL = *subject.URL
	} else {
		// This can happen if the event comes from a private repository
		// and we don't have any API URL values for it.
		return users.User{}, nil
	}
	req, err := s.clV3.NewRequest("GET", apiURL, nil)
	if err != nil {
		return users.User{}, err
	}
	var n struct {
		User   *github.User
		Author *github.User
	}
	_, err = s.clV3.Do(ctx, req, &n)
	if err != nil {
		return users.User{}, err
	}
	if n.User != nil {
		return ghV3User(*n.User), nil
	} else if n.Author != nil {
		// Author is used as fallback, if User isn't present. It can happen on releases, etc.
		return ghV3User(*n.Author), nil
	} else {
		return users.User{}, fmt.Errorf("both User and Author are nil for %q: %v", apiURL, n)
	}
}

// GitHubRouter provides HTML URLs of GitHub notification subjects on https://github.com.
type GitHubRouter struct{}

// IssueURL returns the HTML URL of the specified GitHub issue
// on the https://github.com issue tracker.
func (GitHubRouter) IssueURL(owner, repo string, issueID, commentID uint64) string {
	var fragment string
	if commentID != 0 {
		fragment = fmt.Sprintf("#issuecomment-%d", commentID)
	}
	return fmt.Sprintf("https://github.com/%s/%s/issues/%d%s", owner, repo, issueID, fragment)
}

func (GitHubRouter) PullRequestURL(owner, repo string, prID, commentID uint64) string {
	var fragment string
	if commentID != 0 {
		fragment = fmt.Sprintf("#issuecomment-%d", commentID)
	}
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d%s", owner, repo, prID, fragment)
}

func getCommitURL(subject github.NotificationSubject) (string, error) {
	rs, commit, err := parseSpec(*subject.URL, "commits")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("https://github.com/%s/%s/commit/%s", rs.Owner, rs.Repo, commit), nil
}

// getReleaseURL makes a single API call to get the Release HTMLURL
// from the given releaseAPIURL.
func (s service) getReleaseURL(ctx context.Context, releaseAPIURL string) (string, error) {
	req, err := s.clV3.NewRequest("GET", releaseAPIURL, nil)
	if err != nil {
		return "", err
	}
	var rr github.RepositoryRelease
	_, err = s.clV3.Do(ctx, req, &rr)
	if err != nil {
		return "", err
	}
	return *rr.HTMLURL, nil
}

func getRepositoryInvitationURL(fullName string) string {
	return "https://github.com/" + fullName + "/invitations"
}

func parseIssueSpec(issueAPIURL string) (_ repoSpec, issueID uint64, _ error) {
	rs, id, err := parseSpec(issueAPIURL, "issues")
	if err != nil {
		return repoSpec{}, 0, err
	}
	issueID, err = strconv.ParseUint(id, 10, 64)
	if err != nil {
		return repoSpec{}, 0, err
	}
	return rs, issueID, nil
}

func parsePullRequestSpec(prAPIURL string) (_ repoSpec, prID uint64, _ error) {
	rs, id, err := parseSpec(prAPIURL, "pulls")
	if err != nil {
		return repoSpec{}, 0, err
	}
	prID, err = strconv.ParseUint(id, 10, 64)
	if err != nil {
		return repoSpec{}, 0, err
	}
	return rs, prID, nil
}

func parseSpec(apiURL, specType string) (_ repoSpec, id string, _ error) {
	u, err := url.Parse(apiURL)
	if err != nil {
		return repoSpec{}, "", err
	}
	e := strings.Split(u.Path, "/")
	if len(e) < 5 {
		return repoSpec{}, "", fmt.Errorf("unexpected path (too few elements): %q", u.Path)
	}
	if got, want := e[len(e)-2], specType; got != want {
		return repoSpec{}, "", fmt.Errorf("unexpected path element %q, expecting %q", got, want)
	}
	return repoSpec{Owner: e[len(e)-4], Repo: e[len(e)-3]}, e[len(e)-1], nil
}

type repoSpec struct {
	Owner string
	Repo  string
}

func ghRepoSpec(repo notifications.RepoSpec) (repoSpec, error) {
	// The "github.com/" prefix is expected to be included.
	ghOwnerRepo := strings.Split(repo.URI, "/")
	if len(ghOwnerRepo) != 3 || ghOwnerRepo[0] != "github.com" || ghOwnerRepo[1] == "" || ghOwnerRepo[2] == "" {
		return repoSpec{}, fmt.Errorf(`RepoSpec is not of form "github.com/owner/repo": %q`, repo.URI)
	}
	return repoSpec{
		Owner: ghOwnerRepo[1],
		Repo:  ghOwnerRepo[2],
	}, nil
}

type githubqlActor struct {
	User struct {
		DatabaseID uint64
	} `graphql:"...on User"`
	Bot struct {
		DatabaseID uint64
	} `graphql:"...on Bot"`
	Login     string
	AvatarURL string `graphql:"avatarUrl(size:36)"`
	URL       string
}

func ghActor(actor *githubqlActor) users.User {
	if actor == nil {
		return ghost // Deleted user, replace with https://github.com/ghost.
	}
	return users.User{
		UserSpec: users.UserSpec{
			ID:     actor.User.DatabaseID | actor.Bot.DatabaseID,
			Domain: "github.com",
		},
		Login:     actor.Login,
		AvatarURL: actor.AvatarURL,
		HTMLURL:   actor.URL,
	}
}

func ghV3User(user github.User) users.User {
	if *user.ID == 0 {
		return ghost // Deleted user, replace with https://github.com/ghost.
	}
	return users.User{
		UserSpec: users.UserSpec{
			ID:     uint64(*user.ID),
			Domain: "github.com",
		},
		Login:     *user.Login,
		AvatarURL: avatarURLSize(*user.AvatarURL, 36),
		HTMLURL:   *user.HTMLURL,
	}
}

// avatarURLSize takes avatarURL and sets its "s" query parameter to size.
func avatarURLSize(avatarURL string, size int) string {
	u, err := url.Parse(avatarURL)
	if err != nil {
		return avatarURL
	}
	q := u.Query()
	q.Set("s", strconv.Itoa(size))
	u.RawQuery = q.Encode()
	return u.String()
}

// ghost is https://github.com/ghost, a replacement for deleted users.
var ghost = users.User{
	UserSpec: users.UserSpec{
		ID:     10137,
		Domain: "github.com",
	},
	Login:     "ghost",
	AvatarURL: "https://avatars3.githubusercontent.com/u/10137?v=4&s=36",
	HTMLURL:   "https://github.com/ghost",
}

// TODO: Start using cache whenever possible, remove these.

// ghListNotifications is like github.Client.Activity.ListNotifications,
// but gives caller control over whether cache can be used.
func ghListNotifications(ctx context.Context, cl *github.Client, opt *github.NotificationListOptions, cache bool) ([]*github.Notification, *github.Response, error) {
	u := fmt.Sprintf("notifications")
	u, err := ghAddOptions(u, opt)
	if err != nil {
		return nil, nil, err
	}

	req, err := cl.NewRequest("GET", u, nil)
	if err != nil {
		return nil, nil, err
	}
	if !cache {
		req.Header.Set("Cache-Control", "no-cache")
	}

	var notifications []*github.Notification
	resp, err := cl.Do(ctx, req, &notifications)
	return notifications, resp, err
}

// ghListRepositoryNotifications is like github.Client.Activity.ListRepositoryNotifications,
// but gives caller control over whether cache can be used.
func ghListRepositoryNotifications(ctx context.Context, cl *github.Client, owner, repo string, opt *github.NotificationListOptions, cache bool) ([]*github.Notification, *github.Response, error) {
	u := fmt.Sprintf("repos/%v/%v/notifications", owner, repo)
	u, err := ghAddOptions(u, opt)
	if err != nil {
		return nil, nil, err
	}

	req, err := cl.NewRequest("GET", u, nil)
	if err != nil {
		return nil, nil, err
	}
	if !cache {
		req.Header.Set("Cache-Control", "no-cache")
	}

	var notifications []*github.Notification
	resp, err := cl.Do(ctx, req, &notifications)
	return notifications, resp, err
}

// ghAddOptions adds the parameters in opt as URL query parameters to s.
// opt must be a struct (or a pointer to one) whose fields may contain "url" tags.
func ghAddOptions(s string, opt interface{}) (string, error) {
	if v := reflect.ValueOf(opt); v.Kind() == reflect.Ptr && v.IsNil() {
		return s, nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return s, err
	}
	qs, err := query.Values(opt)
	if err != nil {
		return s, err
	}
	u.RawQuery = qs.Encode()
	return u.String(), nil
}
