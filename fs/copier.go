package fs

import (
	"context"
	"fmt"
	"os"

	"github.com/shurcooL/notifications"
	"github.com/shurcooL/users"
)

var _ notifications.CopierFrom = service{}

func (s service) CopyFrom(ctx context.Context, src notifications.Service, dst users.UserSpec) error {
	// List all accessible notifications.
	ns, err := src.List(ctx, notifications.ListOptions{})
	if err != nil {
		return err
	}

	// Create notificationsDir for dst user in case it doesn't already exist.
	err = s.fs.Mkdir(ctx, notificationsDir(dst), 0755)
	if err != nil && !os.IsExist(err) {
		return err
	}

	fmt.Printf("Copying %v notifications.\n", len(ns))
	for _, n := range ns {
		// Copy notification.
		notification := notification{
			AppID:     n.AppID,
			RepoSpec:  fromRepoSpec(n.RepoSpec),
			ThreadID:  n.ThreadID,
			Title:     n.Title,
			HTMLURL:   n.HTMLURL,
			UpdatedAt: n.UpdatedAt,
			Icon:      fromOcticonID(n.Icon),
			Color:     fromRGB(n.Color),
			Actor:     fromUserSpec(n.Actor.UserSpec),
		}

		// Put in storage.
		err = jsonEncodeFile(s.fs, notificationPath(dst, notificationKey(n.RepoSpec, n.AppID, n.ThreadID)), notification)
		if err != nil {
			return fmt.Errorf("error writing %s: %v", notificationPath(dst, notificationKey(n.RepoSpec, n.AppID, n.ThreadID)), err)
		}
	}

	fmt.Println("All done.")
	return nil
}
