// Package fs implements notifications.Service using a virtual filesystem.
package fs

import (
	"fmt"
	"os"
	"strconv"

	"github.com/shurcooL/notifications"
	"github.com/shurcooL/users"
	"github.com/shurcooL/webdavfs/vfsutil"
	"golang.org/x/net/context"
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

func (s service) List(ctx context.Context, opt interface{}) (notifications.Notifications, error) {
	currentUser, err := s.users.GetAuthenticatedSpec(ctx)
	if err != nil {
		return nil, err
	}
	if currentUser.ID == 0 {
		return nil, os.ErrPermission
	}

	var ns notifications.Notifications
	fis, err := vfsutil.ReadDir(s.fs, notificationsDir(currentUser))
	if err != nil {
		return nil, err
	}
	for _, fi := range fis {
		var n notification
		err := jsonDecodeFile(s.fs, notificationPath(currentUser, fi.Name()), &n)
		if err != nil {
			return nil, fmt.Errorf("error reading %s: %v", notificationPath(currentUser, fi.Name()), err)
		}
		ns = append(ns, notifications.Notification{
			RepoSpec:  n.RepoSpec.RepoSpec(),
			Title:     n.Title,
			Icon:      n.Icon.OcticonID(),
			Color:     n.Color.RGB(),
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
	notifications, err := vfsutil.ReadDir(s.fs, notificationsDir(currentUser))
	if err != nil {
		return 0, err
	}
	return uint64(len(notifications)), nil
}

func (s service) Notify(ctx context.Context, appID string, repo notifications.RepoSpec, threadID uint64, op notifications.Notification) error {
	currentUser, err := s.users.GetAuthenticatedSpec(ctx)
	if err != nil {
		return err
	}
	// TODO: Shouldn't we check if currentUser.ID == 0 here?

	fis, err := vfsutil.ReadDir(s.fs, subscribersDir(repo, appID, threadID))
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	for _, fi := range fis {
		subscriber, err := unmarshalUserSpec(fi.Name())
		if err != nil {
			continue
		}

		if currentUser.ID != 0 && subscriber == currentUser {
			// TODO: Remove this.
			//fmt.Println("DEBUG: not skipping own user, notifying them anyway (for testing)!")

			// Don't notify user of his own actions.
			continue
		}

		n := notification{
			RepoSpec:  fromRepoSpec(repo),
			Title:     op.Title,
			HTMLURL:   op.HTMLURL,
			UpdatedAt: op.UpdatedAt,
			Icon:      fromOcticonID(op.Icon),
			Color:     fromRGB(op.Color),
		}
		err = jsonEncodeFile(s.fs, notificationPath(subscriber, notificationKey(repo, appID, threadID)), n)
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
		err := createEmptyFile(s.fs, subscriberPath(repo, appID, threadID, subscriber))
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
	err = s.fs.RemoveAll(notificationPath(currentUser, notificationKey(repo, appID, threadID)))
	if err != nil {
		return err
	}

	return nil
}

func formatUint64(n uint64) string { return strconv.FormatUint(n, 10) }
