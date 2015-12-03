package notifications

import (
	"html/template"
	"time"

	"golang.org/x/net/context"
	"src.sourcegraph.com/apps/tracker/issues"
)

type Service interface {
	List(ctx context.Context, opt interface{}) ([]Notification, error)

	Subscribe(ctx context.Context, repo issues.RepoSpec, appID string, threadID uint64, actors []issues.UserSpec) error

	MarkRead(ctx context.Context, repo issues.RepoSpec, appID string, threadID uint64) error

	// TODO: This doesn't belong here, does it?
	//CurrentUser(ctx context.Context) (*User, error)
}

type CopierFrom interface {
	CopyFrom(src Service, repo issues.RepoSpec) error // TODO: Consider best place for RepoSpec?
}

type Notification struct {
	RepoSpec  issues.RepoSpec
	Type      string
	Title     string
	HTMLURL   template.URL
	UpdatedAt time.Time
	State     string
}
