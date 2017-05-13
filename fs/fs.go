// Package fs implements notifications.Service using a virtual filesystem.
package fs

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/shurcooL/notifications"
	"github.com/shurcooL/users"
	"github.com/shurcooL/webdavfs/vfsutil"
	"golang.org/x/net/webdav"
)

// NewService creates a virtual filesystem-backed notifications.Service,
// using root for storage.
func NewService(root webdav.FileSystem, users users.Service) notifications.Service {
	return service{
		fs:    root,
		users: users,
	}
}

type service struct {
	fs webdav.FileSystem

	users users.Service
}

func (s service) List(ctx context.Context, opt notifications.ListOptions) (notifications.Notifications, error) {
	currentUser, err := s.users.GetAuthenticatedSpec(ctx)
	if err != nil {
		return nil, err
	}
	if currentUser.ID == 0 {
		return nil, os.ErrPermission
	}

	var ns notifications.Notifications
	fis, err := vfsutil.ReadDir(ctx, s.fs, notificationsDir(currentUser))
	if os.IsNotExist(err) {
		fis = nil
	} else if err != nil {
		return nil, err
	}
	for _, fi := range fis {
		var n notification
		err := jsonDecodeFile(ctx, s.fs, notificationPath(currentUser, fi.Name()), &n)
		if err != nil {
			return nil, fmt.Errorf("error reading %s: %v", notificationPath(currentUser, fi.Name()), err)
		}

		if opt.Repo != nil && n.RepoSpec.RepoSpec() != *opt.Repo {
			continue
		}

		// TODO: Maybe deduce appID and threadID from fi.Name() rather than adding that to encoded JSON...
		ns = append(ns, notifications.Notification{
			AppID:     n.AppID,
			RepoSpec:  n.RepoSpec.RepoSpec(),
			ThreadID:  n.ThreadID,
			RepoURL:   "https://" + n.RepoSpec.URI,
			Title:     n.Title,
			Icon:      n.Icon.OcticonID(),
			Color:     n.Color.RGB(),
			Actor:     s.user(ctx, n.Actor.UserSpec()),
			UpdatedAt: n.UpdatedAt,
			HTMLURL:   n.HTMLURL,
		})
	}

	return ns, nil
}

func (s service) Count(ctx context.Context, opt interface{}) (uint64, error) {
	currentUser, err := s.users.GetAuthenticatedSpec(ctx)
	if err != nil {
		return 0, err
	}
	if currentUser.ID == 0 {
		return 0, os.ErrPermission
	}

	// TODO: Consider reading/parsing entries, in case there's .DS_Store, etc., that should be skipped?
	notifications, err := vfsutil.ReadDir(ctx, s.fs, notificationsDir(currentUser))
	if os.IsNotExist(err) {
		notifications = nil
	} else if err != nil {
		return 0, err
	}
	return uint64(len(notifications)), nil
}

func (s service) Notify(ctx context.Context, appID string, repo notifications.RepoSpec, threadID uint64, nr notifications.NotificationRequest) error {
	currentUser, err := s.users.GetAuthenticatedSpec(ctx)
	if err != nil {
		return err
	}
	if currentUser.ID == 0 {
		return os.ErrPermission
	}

	type subscription struct {
		Participating bool
	}
	var subscribers = make(map[users.UserSpec]subscription)

	// Repo watchers.
	fis, err := vfsutil.ReadDir(ctx, s.fs, subscribersDir(repo, "", 0))
	if os.IsNotExist(err) {
		fis = nil
	} else if err != nil {
		return err
	}
	for _, fi := range fis {
		if fi.IsDir() {
			continue
		}
		subscriber, err := unmarshalUserSpec(fi.Name())
		if err != nil {
			continue
		}
		subscribers[subscriber] = subscription{Participating: false}
	}

	// Thread subscribers. Iterate over them after repo watchers,
	// so that their participating status takes higher precedence.
	fis, err = vfsutil.ReadDir(ctx, s.fs, subscribersDir(repo, appID, threadID))
	if os.IsNotExist(err) {
		fis = nil
	} else if err != nil {
		return err
	}
	for _, fi := range fis {
		if fi.IsDir() {
			continue
		}
		subscriber, err := unmarshalUserSpec(fi.Name())
		if err != nil {
			continue
		}
		subscribers[subscriber] = subscription{Participating: true}
	}

	for subscriber, subscription := range subscribers {
		if currentUser.ID != 0 && subscriber == currentUser {
			// Don't notify user of his own actions.
			continue
		}

		// Create notificationsDir for subscriber in case it doesn't already exist.
		err = s.fs.Mkdir(ctx, notificationsDir(subscriber), 0755)
		if err != nil && !os.IsExist(err) {
			return err
		}

		// TODO: Maybe deduce appID and threadID from fi.Name() rather than adding that to encoded JSON...
		n := notification{
			AppID:     appID,
			RepoSpec:  fromRepoSpec(repo),
			ThreadID:  threadID,
			Title:     nr.Title,
			HTMLURL:   nr.HTMLURL,
			UpdatedAt: nr.UpdatedAt,
			Icon:      fromOcticonID(nr.Icon),
			Color:     fromRGB(nr.Color),
			Actor:     fromUserSpec(nr.Actor),

			Participating: subscription.Participating,
		}
		err = jsonEncodeFile(ctx, s.fs, notificationPath(subscriber, notificationKey(repo, appID, threadID)), n)
		// TODO: Maybe in future read previous value, and use it to preserve some fields, like earliest HTML URL.
		//       Maybe that shouldn't happen here though.
		if err != nil {
			return fmt.Errorf("error writing %s: %v", notificationPath(subscriber, notificationKey(repo, appID, threadID)), err)
		}
	}

	return nil
}

func (s service) Subscribe(ctx context.Context, appID string, repo notifications.RepoSpec, threadID uint64, subscribers []users.UserSpec) error {
	currentUser, err := s.users.GetAuthenticatedSpec(ctx)
	if err != nil {
		return err
	}
	if currentUser.ID == 0 {
		return os.ErrPermission
	}

	for _, subscriber := range subscribers {
		err := createEmptyFile(ctx, s.fs, subscriberPath(repo, appID, threadID, subscriber))
		if err != nil {
			return err
		}
	}

	return nil
}

func (s service) MarkRead(ctx context.Context, appID string, repo notifications.RepoSpec, threadID uint64) error {
	currentUser, err := s.users.GetAuthenticatedSpec(ctx)
	if err != nil {
		return err
	}
	if currentUser.ID == 0 {
		return os.ErrPermission
	}

	// TODO: Move notification instead of outright removing, maybe?
	err = s.fs.RemoveAll(ctx, notificationPath(currentUser, notificationKey(repo, appID, threadID)))
	if err != nil {
		return err
	}
	// THINK: Consider using the dir-less vfs abstraction for doing this implicitly? Less code here.
	// If the user has no more notifications left, remove their empty directory.
	switch notifications, err := vfsutil.ReadDir(ctx, s.fs, notificationsDir(currentUser)); {
	case err != nil && !os.IsNotExist(err):
		return err
	case err == nil && len(notifications) == 0:
		err := s.fs.RemoveAll(ctx, notificationsDir(currentUser))
		if err != nil {
			return err
		}
	}

	return nil
}

func (s service) MarkAllRead(ctx context.Context, repo notifications.RepoSpec) error {
	currentUser, err := s.users.GetAuthenticatedSpec(ctx)
	if err != nil {
		return err
	}
	if currentUser.ID == 0 {
		return os.ErrPermission
	}

	// Iterate all user's notifications.
	fis, err := vfsutil.ReadDir(ctx, s.fs, notificationsDir(currentUser))
	if os.IsNotExist(err) {
		fis = nil
	} else if err != nil {
		return err
	}
	for _, fi := range fis {
		var n notification
		err := jsonDecodeFile(ctx, s.fs, notificationPath(currentUser, fi.Name()), &n)
		if err != nil {
			log.Printf("error reading %s: %v\n", notificationPath(currentUser, fi.Name()), err)
			continue
		}

		// Skip notifications whose repo doesn't match.
		if n.RepoSpec.RepoSpec() != repo {
			continue
		}

		// TODO: Move notification instead of outright removing, maybe?
		err = s.fs.RemoveAll(ctx, notificationPath(currentUser, notificationKey(repo, n.AppID, n.ThreadID)))
		if err != nil {
			return err
		}
	}

	// THINK: Consider using the dir-less vfs abstraction for doing this implicitly? Less code here.
	// If the user has no more notifications left, remove their empty directory.
	switch notifications, err := vfsutil.ReadDir(ctx, s.fs, notificationsDir(currentUser)); {
	case err != nil && !os.IsNotExist(err):
		return err
	case err == nil && len(notifications) == 0:
		err := s.fs.RemoveAll(ctx, notificationsDir(currentUser))
		if err != nil {
			return err
		}
	}

	return nil
}

func (s service) user(ctx context.Context, user users.UserSpec) users.User {
	u, err := s.users.Get(ctx, user)
	if err != nil {
		return users.User{
			UserSpec:  user,
			Login:     fmt.Sprintf("%d@%s", user.ID, user.Domain),
			AvatarURL: "",
			HTMLURL:   "",
		}
	}
	return u
}
