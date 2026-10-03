package commands

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

// Avatars rarely change and a CDN link keeps working until they do, so a slow refresh is enough.
const friendsEvery = 6 * time.Hour

// friends maps a sibling's application id to its avatar. The zero value is empty, and a friend
// missing from it shows its card without an icon rather than Discord's default avatar, which would
// read as a real but wrong face.
type friends struct {
	mu sync.RWMutex
	m  map[snowflake.ID]string
}

func (f *friends) avatar(app snowflake.ID) string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.m[app]
}

// RunFriends fetches each sibling's avatar by its application id, which is its bot user's id for
// any application made since bots got one. A failed fetch keeps that friend's last avatar.
func (c *Commands) RunFriends(ctx context.Context) error {
	t := time.NewTicker(friendsEvery)
	defer t.Stop()
	for {
		m := make(map[snowflake.ID]string, len(c.siblings))
		for _, s := range c.siblings {
			if s.App == c.client.ApplicationID {
				continue
			}
			u, err := c.client.Rest.GetUser(s.App, rest.WithCtx(ctx))
			switch {
			case err != nil:
				slog.Warn("friends: fetching avatar", slog.String("friend", s.Name), slog.Any("app", s.App), slog.Any("err", err))
				m[s.App] = c.friends.avatar(s.App)
			case u.AvatarURL() != nil:
				m[s.App] = *u.AvatarURL()
			}
		}
		c.friends.mu.Lock()
		c.friends.m = m
		c.friends.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}
