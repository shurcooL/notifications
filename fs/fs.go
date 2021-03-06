// Package fs implements notifications.Service using a virtual filesystem.
package fs

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/shurcooL/notifications"
	"github.com/shurcooL/users"
	"github.com/shurcooL/webdavfs/vfsutil"
	"golang.org/x/net/webdav"
)

// NewService creates a virtual filesystem-backed notifications.Service,
// using root for storage.
func NewService(root webdav.FileSystem, users users.Service) notifications.Service {
	return &service{
		fs:    root,
		users: users,
	}
}

type service struct {
	fsMu sync.RWMutex
	fs   webdav.FileSystem

	users users.Service
}

func (s *service) List(ctx context.Context, opt notifications.ListOptions) (notifications.Notifications, error) {
	currentUser, err := s.users.GetAuthenticatedSpec(ctx)
	if err != nil {
		return nil, err
	}
	if currentUser.ID == 0 {
		return nil, os.ErrPermission
	}

	s.fsMu.RLock()
	defer s.fsMu.RUnlock()

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

		// TODO: Maybe deduce threadType and threadID from fi.Name() rather than adding that to encoded JSON...
		ns = append(ns, notifications.Notification{
			RepoSpec:   n.RepoSpec.RepoSpec(),
			ThreadType: n.ThreadType,
			ThreadID:   n.ThreadID,
			Title:      n.Title,
			Icon:       n.Icon.OcticonID(),
			Color:      n.Color.RGB(),
			Actor:      s.user(ctx, n.Actor.UserSpec()),
			UpdatedAt:  n.UpdatedAt,
			HTMLURL:    n.HTMLURL,
		})
	}

	if opt.All {
		fis, err := vfsutil.ReadDir(ctx, s.fs, readDir(currentUser))
		if os.IsNotExist(err) {
			fis = nil
		} else if err != nil {
			return nil, err
		}
		for _, fi := range fis {
			var n notification
			err := jsonDecodeFile(ctx, s.fs, readPath(currentUser, fi.Name()), &n)
			if err != nil {
				return nil, fmt.Errorf("error reading %s: %v", readPath(currentUser, fi.Name()), err)
			}

			// Delete and skip old read notifications.
			if time.Since(n.UpdatedAt) > 30*24*time.Hour {
				err := s.fs.RemoveAll(ctx, readPath(currentUser, fi.Name()))
				if err != nil {
					return nil, err
				}
				continue
			}

			if opt.Repo != nil && n.RepoSpec.RepoSpec() != *opt.Repo {
				continue
			}

			// TODO: Maybe deduce threadType and threadID from fi.Name() rather than adding that to encoded JSON...
			ns = append(ns, notifications.Notification{
				RepoSpec:   n.RepoSpec.RepoSpec(),
				ThreadType: n.ThreadType,
				ThreadID:   n.ThreadID,
				Title:      n.Title,
				Icon:       n.Icon.OcticonID(),
				Color:      n.Color.RGB(),
				Actor:      s.user(ctx, n.Actor.UserSpec()),
				UpdatedAt:  n.UpdatedAt,
				Read:       true,
				HTMLURL:    n.HTMLURL,
			})
		}

		// THINK: Consider using the dir-less vfs abstraction for doing this implicitly? Less code here.
		// If the user has no more read notifications left, remove the empty directory.
		switch notifications, err := vfsutil.ReadDir(ctx, s.fs, readDir(currentUser)); {
		case err != nil && !os.IsNotExist(err):
			return nil, err
		case err == nil && len(notifications) == 0:
			err := s.fs.RemoveAll(ctx, readDir(currentUser))
			if err != nil {
				return nil, err
			}
		}
	}

	return ns, nil
}

func (s *service) Count(ctx context.Context, opt interface{}) (uint64, error) {
	currentUser, err := s.users.GetAuthenticatedSpec(ctx)
	if err != nil {
		return 0, err
	}
	if currentUser.ID == 0 {
		return 0, os.ErrPermission
	}

	s.fsMu.RLock()
	defer s.fsMu.RUnlock()

	// TODO: Consider reading/parsing entries, in case there's .DS_Store, etc., that should be skipped?
	notifications, err := vfsutil.ReadDir(ctx, s.fs, notificationsDir(currentUser))
	if os.IsNotExist(err) {
		notifications = nil
	} else if err != nil {
		return 0, err
	}
	return uint64(len(notifications)), nil
}

func (s *service) Notify(ctx context.Context, repo notifications.RepoSpec, threadType string, threadID uint64, nr notifications.NotificationRequest) error {
	currentUser, err := s.users.GetAuthenticatedSpec(ctx)
	if err != nil {
		return err
	}
	if currentUser.ID == 0 {
		return os.ErrPermission
	}

	s.fsMu.Lock()
	defer s.fsMu.Unlock()

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
	fis, err = vfsutil.ReadDir(ctx, s.fs, subscribersDir(repo, threadType, threadID))
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

		// Delete read notification with same key, if any.
		err = s.fs.RemoveAll(ctx, readPath(subscriber, notificationKey(repo, threadType, threadID)))
		if err != nil && !os.IsNotExist(err) {
			return err
		}

		// Create notificationsDir for subscriber in case it doesn't already exist.
		err = s.fs.Mkdir(ctx, notificationsDir(subscriber), 0755)
		if err != nil && !os.IsExist(err) {
			return err
		}

		// TODO: Maybe deduce threadType and threadID from fi.Name() rather than adding that to encoded JSON...
		n := notification{
			RepoSpec:   fromRepoSpec(repo),
			ThreadType: threadType,
			ThreadID:   threadID,
			Title:      nr.Title,
			HTMLURL:    nr.HTMLURL,
			UpdatedAt:  nr.UpdatedAt,
			Icon:       fromOcticonID(nr.Icon),
			Color:      fromRGB(nr.Color),
			Actor:      fromUserSpec(nr.Actor), // TODO: Why not use current user?

			Participating: subscription.Participating,
		}
		err = jsonEncodeFile(ctx, s.fs, notificationPath(subscriber, notificationKey(repo, threadType, threadID)), n)
		// TODO: Maybe in future read previous value, and use it to preserve some fields, like earliest HTML URL.
		//       Maybe that shouldn't happen here though.
		if err != nil {
			return fmt.Errorf("error writing %s: %v", notificationPath(subscriber, notificationKey(repo, threadType, threadID)), err)
		}
	}

	return nil
}

func (s *service) Subscribe(ctx context.Context, repo notifications.RepoSpec, threadType string, threadID uint64, subscribers []users.UserSpec) error {
	currentUser, err := s.users.GetAuthenticatedSpec(ctx)
	if err != nil {
		return err
	}
	if currentUser.ID == 0 {
		return os.ErrPermission
	}

	s.fsMu.Lock()
	defer s.fsMu.Unlock()

	for _, subscriber := range subscribers {
		err := createEmptyFile(ctx, s.fs, subscriberPath(repo, threadType, threadID, subscriber))
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *service) MarkRead(ctx context.Context, repo notifications.RepoSpec, threadType string, threadID uint64) error {
	currentUser, err := s.users.GetAuthenticatedSpec(ctx)
	if err != nil {
		return err
	}
	if currentUser.ID == 0 {
		return os.ErrPermission
	}

	s.fsMu.Lock()
	defer s.fsMu.Unlock()

	// Return early if the notification doesn't exist, before creating readDir for currentUser.
	key := notificationKey(repo, threadType, threadID)
	_, err = vfsutil.Stat(ctx, s.fs, notificationPath(currentUser, key))
	if os.IsNotExist(err) {
		return nil
	}

	// Create readDir for currentUser in case it doesn't already exist.
	err = s.fs.Mkdir(ctx, readDir(currentUser), 0755)
	if err != nil && !os.IsExist(err) {
		return err
	}
	// Move notification to read directory.
	err = s.fs.Rename(ctx, notificationPath(currentUser, key), readPath(currentUser, key))
	if err != nil {
		return err
	}

	// THINK: Consider using the dir-less vfs abstraction for doing this implicitly? Less code here.
	// If the user has no more unread notifications left, remove the empty directory.
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

func (s *service) MarkAllRead(ctx context.Context, repo notifications.RepoSpec) error {
	currentUser, err := s.users.GetAuthenticatedSpec(ctx)
	if err != nil {
		return err
	}
	if currentUser.ID == 0 {
		return os.ErrPermission
	}

	s.fsMu.Lock()
	defer s.fsMu.Unlock()

	// Iterate all user's notifications.
	fis, err := vfsutil.ReadDir(ctx, s.fs, notificationsDir(currentUser))
	if os.IsNotExist(err) {
		fis = nil
	} else if err != nil {
		return err
	}
	madeReadDir := false
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

		// Create readDir for currentUser in case it doesn't already exist.
		if !madeReadDir {
			err = s.fs.Mkdir(ctx, readDir(currentUser), 0755)
			if err != nil && !os.IsExist(err) {
				return err
			}
			madeReadDir = true
		}
		// Move notification to read directory.
		key := notificationKey(repo, n.ThreadType, n.ThreadID)
		err = s.fs.Rename(ctx, notificationPath(currentUser, key), readPath(currentUser, key))
		if err != nil {
			return err
		}
	}

	// THINK: Consider using the dir-less vfs abstraction for doing this implicitly? Less code here.
	// If the user has no more unread notifications left, remove the empty directory.
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

func (s *service) user(ctx context.Context, user users.UserSpec) users.User {
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
