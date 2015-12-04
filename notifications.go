package notifications

import (
	"html/template"
	"time"

	"golang.org/x/net/context"
	"src.sourcegraph.com/apps/tracker/issues"
)

type Service interface {
	List(ctx context.Context, opt interface{}) ([]Notification, error)

	Subscribe(ctx context.Context, appID string, repo issues.RepoSpec, threadID uint64, subscribers []issues.UserSpec) error

	MarkRead(ctx context.Context, appID string, repo issues.RepoSpec, threadID uint64) error

	// TODO.
	Create(ctx context.Context, appID string, repo issues.RepoSpec, threadID uint64, notification Notification) error

	// TODO: This doesn't belong here, does it?
	//CurrentUser(ctx context.Context) (*User, error)
}

type CopierFrom interface {
	CopyFrom(src Service, repo issues.RepoSpec) error // TODO: Consider best place for RepoSpec?
}

type Notification struct {
	RepoSpec  issues.RepoSpec
	Type      string // TODO: Give it a named type? Or something?
	Title     string
	HTMLURL   template.URL
	UpdatedAt time.Time
	State     string // TODO: Change to icon, etc.?
}

// Notifications implements sort.Interface.
type Notifications []Notification

func (s Notifications) Len() int           { return len(s) }
func (s Notifications) Less(i, j int) bool { return !s[i].UpdatedAt.Before(s[j].UpdatedAt) }
func (s Notifications) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
