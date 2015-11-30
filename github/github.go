// Package github implements notifications.Service using GitHub API client.
package github

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/google/go-github/github"
	"golang.org/x/net/context"
	"src.sourcegraph.com/apps/notifications/notifications"
	"src.sourcegraph.com/apps/tracker/issues"
)

// NewService creates a GitHub-backed notifications.Service using given GitHub client.
// At this time it infers the current user from the client (its authentication info), and cannot be used to serve multiple users.
func NewService(client *github.Client) notifications.Service {
	if client == nil {
		client = github.NewClient(nil)
	}

	s := service{
		cl: client,
	}

	if user, _, err := client.Users.Get(""); err == nil {
		u := ghUser(user)
		s.currentUser = &u
		s.currentUserErr = nil
	} else if ghErr, ok := err.(*github.ErrorResponse); ok && ghErr.Response.StatusCode == http.StatusUnauthorized {
		// There's no authenticated user.
		s.currentUser = nil
		s.currentUserErr = nil
	} else {
		s.currentUser = nil
		s.currentUserErr = err
	}

	return s
}

type service struct {
	cl *github.Client

	currentUser    *issues.User
	currentUserErr error
}

func (s service) CurrentUser(_ context.Context) (*issues.User, error) {
	return s.currentUser, s.currentUserErr
}

type repoSpec struct {
	Owner string
	Repo  string
}

func ghRepoSpec(repo issues.RepoSpec) repoSpec {
	ownerRepo := strings.Split(repo.URI, "/")
	if len(ownerRepo) != 2 {
		panic(fmt.Errorf(`RepoSpec is not of form "owner/repo": %v`, repo))
	}
	return repoSpec{
		Owner: ownerRepo[0],
		Repo:  ownerRepo[1],
	}
}

func ghUser(user *github.User) issues.User {
	return issues.User{
		UserSpec: issues.UserSpec{
			ID:     uint64(*user.ID),
			Domain: "github.com",
		},
		Login:     *user.Login,
		AvatarURL: template.URL(*user.AvatarURL),
		HTMLURL:   template.URL(*user.HTMLURL),
	}
}
