package fs_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shurcooL/notifications"
	"github.com/shurcooL/notifications/fs"
	"github.com/shurcooL/users"
	"golang.org/x/net/webdav"
)

func Test(t *testing.T) {
	mem := webdav.NewMemFS()
	err := mem.Mkdir(context.Background(), "notifications", 0755)
	if err != nil {
		t.Fatal(err)
	}
	err = mem.Mkdir(context.Background(), "read", 0755)
	if err != nil {
		t.Fatal(err)
	}
	usersService := &mockUsers{Current: users.UserSpec{ID: 1, Domain: "example.org"}}
	s := fs.NewService(mem, usersService)

	// List notifications.
	ns, err := s.List(context.Background(), notifications.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 0 {
		t.Errorf("want no notifications, got: %+v", ns)
	}

	// Subscribe target user to all issues.
	err = s.Subscribe(context.Background(), notifications.RepoSpec{URI: "repo"}, "issues", 1,
		[]users.UserSpec{{ID: 1, Domain: "example.org"}})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Subscribe(context.Background(), notifications.RepoSpec{URI: "repo"}, "issues", 2,
		[]users.UserSpec{{ID: 1, Domain: "example.org"}})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Subscribe(context.Background(), notifications.RepoSpec{URI: "repo"}, "issues", 3,
		[]users.UserSpec{{ID: 1, Domain: "example.org"}})
	if err != nil {
		t.Fatal(err)
	}

	// Make a notification as another user.
	usersService.Current.ID = 2
	err = s.Notify(context.Background(), notifications.RepoSpec{URI: "repo"}, "issues", 1,
		notifications.NotificationRequest{
			Title:     "Issue 1",
			Actor:     users.UserSpec{ID: 1, Domain: "example.org"},
			UpdatedAt: time.Now(),
		})
	if err != nil {
		t.Fatal(err)
	}
	usersService.Current.ID = 1

	// List notifications.
	ns, err = s.List(context.Background(), notifications.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 1 || ns[0].Read || ns[0].Title != "Issue 1" {
		t.Errorf(`want 1 unread notification "Issue 1", got: %+v`, ns)
	}

	// Mark it read.
	err = s.MarkRead(context.Background(), notifications.RepoSpec{URI: "repo"}, "issues", 1)
	if err != nil {
		t.Fatal(err)
	}

	// List notifications.
	ns, err = s.List(context.Background(), notifications.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 0 {
		t.Errorf("want no notifications, got: %+v", ns)
	}
	ns, err = s.List(context.Background(), notifications.ListOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 1 || !ns[0].Read || ns[0].Title != "Issue 1" {
		t.Errorf(`want 1 read notification "Issue 1", got: %+v`, ns)
	}

	// Make 2 new notifications as another user.
	usersService.Current.ID = 2
	err = s.Notify(context.Background(), notifications.RepoSpec{URI: "repo"}, "issues", 2,
		notifications.NotificationRequest{
			Title:     "Issue 2",
			Actor:     users.UserSpec{ID: 1, Domain: "example.org"},
			UpdatedAt: time.Now(),
		})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Notify(context.Background(), notifications.RepoSpec{URI: "repo"}, "issues", 3,
		notifications.NotificationRequest{
			Title:     "Issue 3",
			Actor:     users.UserSpec{ID: 1, Domain: "example.org"},
			UpdatedAt: time.Now(),
		})
	if err != nil {
		t.Fatal(err)
	}
	usersService.Current.ID = 1

	// List notifications.
	ns, err = s.List(context.Background(), notifications.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 2 {
		t.Errorf("want 2 notifications, got: %+v", ns)
	}
	ns, err = s.List(context.Background(), notifications.ListOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 3 {
		t.Errorf("want 3 notifications, got: %+v", ns)
	}

	// Mark all read.
	err = s.MarkAllRead(context.Background(), notifications.RepoSpec{URI: "repo"})
	if err != nil {
		t.Fatal(err)
	}

	// List notifications.
	ns, err = s.List(context.Background(), notifications.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 0 {
		t.Errorf("want no notifications, got: %+v", ns)
	}
	ns, err = s.List(context.Background(), notifications.ListOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 3 {
		t.Errorf("want 3 notifications, got %d: %+v", len(ns), ns)
	}

	// Repeat a notification as another user.
	usersService.Current.ID = 2
	err = s.Notify(context.Background(), notifications.RepoSpec{URI: "repo"}, "issues", 1,
		notifications.NotificationRequest{
			Title:     "Issue 1",
			Actor:     users.UserSpec{ID: 1, Domain: "example.org"},
			UpdatedAt: time.Now(),
		})
	if err != nil {
		t.Fatal(err)
	}
	usersService.Current.ID = 1

	// List notifications.
	ns, err = s.List(context.Background(), notifications.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 1 || ns[0].Read || ns[0].Title != "Issue 1" {
		t.Errorf(`want 1 unread notification "Issue 1", got: %+v`, ns)
	}
	ns, err = s.List(context.Background(), notifications.ListOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 3 {
		t.Errorf("want 3 notifications, got %d: %+v", len(ns), ns)
	}
}

type mockUsers struct {
	Current users.UserSpec
	users.Service
}

func (mockUsers) Get(_ context.Context, user users.UserSpec) (users.User, error) {
	switch {
	case user == users.UserSpec{ID: 1, Domain: "example.org"}:
		return users.User{
			UserSpec: user,
			Login:    "gopher1",
			Name:     "Gopher One",
			Email:    "gopher1@example.org",
		}, nil
	case user == users.UserSpec{ID: 2, Domain: "example.org"}:
		return users.User{
			UserSpec: user,
			Login:    "gopher2",
			Name:     "Gopher Two",
			Email:    "gopher2@example.org",
		}, nil
	default:
		return users.User{}, fmt.Errorf("user %v not found", user)
	}
}

func (m mockUsers) GetAuthenticatedSpec(context.Context) (users.UserSpec, error) {
	return m.Current, nil
}

func (m mockUsers) GetAuthenticated(ctx context.Context) (users.User, error) {
	userSpec, err := m.GetAuthenticatedSpec(ctx)
	if err != nil {
		return users.User{}, err
	}
	if userSpec.ID == 0 {
		return users.User{}, nil
	}
	return m.Get(ctx, userSpec)
}
