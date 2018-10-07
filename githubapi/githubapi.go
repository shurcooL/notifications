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

	"dmitri.shuralyov.com/route/github"
	githubv3 "github.com/google/go-github/github"
	"github.com/google/go-querystring/query"
	"github.com/gregjones/httpcache"
	"github.com/shurcooL/githubv4"
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
// If router is nil, github.DotCom router is used, which links to subjects on github.com.
func NewService(clientV3 *githubv3.Client, clientV4 *githubv4.Client, router github.Router) notifications.Service {
	if router == nil {
		router = github.DotCom{}
	}
	return service{
		clV3: clientV3,
		clV4: clientV4,
		rtr:  router,
	}
}

type service struct {
	clV3 *githubv3.Client // GitHub REST API v3 client.
	clV4 *githubv4.Client // GitHub GraphQL API v4 client.
	rtr  github.Router
}

func (s service) List(ctx context.Context, opt notifications.ListOptions) (notifications.Notifications, error) {
	var ns []notifications.Notification

	ghOpt := &githubv3.NotificationListOptions{
		All:         opt.All,
		ListOptions: githubv3.ListOptions{PerPage: 100},
	}
	var ghNotifications []*githubv3.Notification
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
		//       this possible. See https://github.com/shurcooL/githubv4/issues/17.

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
						State    githubv4.IssueState
						Author   *githubV4Actor
						Comments struct {
							Nodes []struct {
								Author     *githubV4Actor
								DatabaseID uint64
							}
						} `graphql:"comments(last:1)"`
					} `graphql:"issue(number:$issueNumber)"`
				} `graphql:"repository(owner:$repositoryOwner,name:$repositoryName)"`
			}
			variables := map[string]interface{}{
				"repositoryOwner": githubv4.String(rs.Owner),
				"repositoryName":  githubv4.String(rs.Repo),
				"issueNumber":     githubv4.Int(issueID),
			}
			err = s.clV4.Query(ctx, &q, variables)
			if err != nil {
				return ns, err
			}
			switch q.Repository.Issue.State {
			case githubv4.IssueStateOpen:
				notification.Icon = "issue-opened"
				notification.Color = notifications.RGB{R: 0x6c, G: 0xc6, B: 0x44} // Green.
			case githubv4.IssueStateClosed:
				notification.Icon = "issue-closed"
				notification.Color = notifications.RGB{R: 0xbd, G: 0x2c, B: 0x00} // Red.
			}
			switch len(q.Repository.Issue.Comments.Nodes) {
			case 0:
				notification.Actor = ghActor(q.Repository.Issue.Author)
				notification.HTMLURL = s.rtr.IssueURL(ctx, rs.Owner, rs.Repo, issueID)
			case 1:
				notification.Actor = ghActor(q.Repository.Issue.Comments.Nodes[0].Author)
				notification.HTMLURL = s.rtr.IssueCommentURL(ctx, rs.Owner, rs.Repo, issueID, q.Repository.Issue.Comments.Nodes[0].DatabaseID)
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
						State    githubv4.PullRequestState
						Author   *githubV4Actor
						Comments struct {
							Nodes []struct {
								Author     *githubV4Actor
								DatabaseID uint64
								CreatedAt  time.Time
							}
						} `graphql:"comments(last:1)"`
						Reviews struct {
							Nodes []struct {
								Author     *githubV4Actor
								DatabaseID uint64
								CreatedAt  time.Time
							}
						} `graphql:"reviews(last:1)"`
					} `graphql:"pullRequest(number:$prNumber)"`
				} `graphql:"repository(owner:$repositoryOwner,name:$repositoryName)"`
			}
			variables := map[string]interface{}{
				"repositoryOwner": githubv4.String(rs.Owner),
				"repositoryName":  githubv4.String(rs.Repo),
				"prNumber":        githubv4.Int(prID),
			}
			err = s.clV4.Query(ctx, &q, variables)
			if err != nil {
				return ns, err
			}
			notification.Icon = "git-pull-request"
			switch q.Repository.PullRequest.State {
			case githubv4.PullRequestStateOpen:
				notification.Color = notifications.RGB{R: 0x6c, G: 0xc6, B: 0x44} // Green.
			case githubv4.PullRequestStateClosed:
				notification.Color = notifications.RGB{R: 0xbd, G: 0x2c, B: 0x00} // Red.
			case githubv4.PullRequestStateMerged:
				notification.Color = notifications.RGB{R: 0x6e, G: 0x54, B: 0x94} // Purple.
			}
			switch c, r := q.Repository.PullRequest.Comments.Nodes, q.Repository.PullRequest.Reviews.Nodes; {
			case len(c) == 0 && len(r) == 0:
				notification.Actor = ghActor(q.Repository.PullRequest.Author)
				notification.HTMLURL = s.rtr.PullRequestURL(ctx, rs.Owner, rs.Repo, prID)
			case len(c) == 1 && len(r) == 0:
				notification.Actor = ghActor(c[0].Author)
				notification.HTMLURL = s.rtr.PullRequestCommentURL(ctx, rs.Owner, rs.Repo, prID, c[0].DatabaseID)
			case len(c) == 0 && len(r) == 1:
				notification.Actor = ghActor(r[0].Author)
				notification.HTMLURL = s.rtr.PullRequestReviewURL(ctx, rs.Owner, rs.Repo, prID, r[0].DatabaseID)
			case len(c) == 1 && len(r) == 1:
				// Use the later of the two.
				if c[0].CreatedAt.After(r[0].CreatedAt) {
					notification.Actor = ghActor(c[0].Author)
					notification.HTMLURL = s.rtr.PullRequestCommentURL(ctx, rs.Owner, rs.Repo, prID, c[0].DatabaseID)
				} else {
					notification.Actor = ghActor(r[0].Author)
					notification.HTMLURL = s.rtr.PullRequestReviewURL(ctx, rs.Owner, rs.Repo, prID, r[0].DatabaseID)
				}
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
	ghOpt := &githubv3.NotificationListOptions{ListOptions: githubv3.ListOptions{PerPage: 1}}
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

	var alsoLookWithoutCache bool

	// First, iterate over all pages of notifications, looking for the specified notification.
	// It's okay to use with-cache client here, because we don't mind seeing read notifications
	// for the purposes of MarkRead. They'll be skipped if the notification ID doesn't match.
	ghOpt := &githubv3.NotificationListOptions{ListOptions: githubv3.ListOptions{PerPage: 100}}
	for {
		cached, resp, err := ghListRepositoryNotifications(ctx, s.clV3, repo.Owner, repo.Repo, ghOpt, true)
		if err != nil {
			return fmt.Errorf("failed to ListRepositoryNotifications: %v", err)
		}
		if _, ok := resp.Response.Header[httpcache.XFromCache]; ok {
			// If and only if any of the responses come from cache,
			// we'll want to look again without cache before giving up.
			alsoLookWithoutCache = true
		}
		if notif, err := findNotification(cached, threadType, threadID); err != nil {
			return err
		} else if notif != nil {
			// Found a matching notification, mark it read.
			return s.markRead(ctx, notif)
		}
		if resp.NextPage == 0 {
			break
		}
		ghOpt.Page = resp.NextPage
	}

	// However, there are sometimes caching issues causing stale repository notifications
	// to be retrieved from cache, and a legitimate existing notification is not marked read.
	// So fall back to skipping cache, if we can't find a notification and the response
	// we got was from cache (rather than origin server).
	if alsoLookWithoutCache {
		ghOpt.Page = 0
		for {
			uncached, resp, err := ghListRepositoryNotifications(ctx, s.clV3, repo.Owner, repo.Repo, ghOpt, false)
			if err != nil {
				return fmt.Errorf("failed to ListRepositoryNotifications: %v", err)
			}
			if notif, err := findNotification(uncached, threadType, threadID); err != nil {
				return err
			} else if notif != nil {
				// Found a matching notification, mark it read.
				log.Printf(`MarkRead: did not find notification %s/%s %s %d within cached notifications, but did find within uncached ones`, repo.Owner, repo.Repo, threadType, threadID)
				return s.markRead(ctx, notif)
			}
			if resp.NextPage == 0 {
				break
			}
			ghOpt.Page = resp.NextPage
		}
	}

	// Didn't find any matching notification to mark read.
	// Nothing to do.
	return nil
}

func (s service) markRead(ctx context.Context, n *githubv3.Notification) error {
	_, err := s.clV3.Activity.MarkThreadRead(ctx, *n.ID)
	if err != nil {
		return fmt.Errorf("failed to MarkThreadRead: %v", err)
	}
	return nil
}

// findNotification tries to find a notification that matches
// the provided threadType and threadID.
// threadType must be one of "Issue" or "PullRequest".
// It returns nil if no matching notification is found, and
// any error encountered.
func findNotification(ns []*githubv3.Notification, threadType string, threadID uint64) (*githubv3.Notification, error) {
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
func (s service) getNotificationActor(ctx context.Context, subject githubv3.NotificationSubject) (users.User, error) {
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
		User   *githubv3.User
		Author *githubv3.User
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

func getCommitURL(subject githubv3.NotificationSubject) (string, error) {
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
	var rr githubv3.RepositoryRelease
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

type githubV4Actor struct {
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

func ghActor(actor *githubV4Actor) users.User {
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

func ghV3User(user githubv3.User) users.User {
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

// ghListNotifications is like githubv3.Client.Activity.ListNotifications,
// but gives caller control over whether cache can be used.
func ghListNotifications(ctx context.Context, cl *githubv3.Client, opt *githubv3.NotificationListOptions, cache bool) ([]*githubv3.Notification, *githubv3.Response, error) {
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

	var notifications []*githubv3.Notification
	resp, err := cl.Do(ctx, req, &notifications)
	return notifications, resp, err
}

// ghListRepositoryNotifications is like githubv3.Client.Activity.ListRepositoryNotifications,
// but gives caller control over whether cache can be used.
func ghListRepositoryNotifications(ctx context.Context, cl *githubv3.Client, owner, repo string, opt *githubv3.NotificationListOptions, cache bool) ([]*githubv3.Notification, *githubv3.Response, error) {
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

	var notifications []*githubv3.Notification
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
