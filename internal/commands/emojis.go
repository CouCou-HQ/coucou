package commands

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
)

// Discord sends no event when an app emoji is added or deleted, so the list is fetched again on a
// timer. One deleted in between is still mentioned until the next fetch.
const emojisEvery = 10 * time.Minute

// emojis maps a sound name to the mention of the app emoji with the same name. The zero value is
// empty, and an empty map leaves every sound as text.
type emojis struct {
	mu sync.RWMutex
	m  map[string]string
}

func (e *emojis) set(list []discord.Emoji) {
	m := make(map[string]string, len(list))
	for _, em := range list {
		m[em.Name] = em.Mention()
	}
	e.mu.Lock()
	e.m = m
	e.mu.Unlock()
}

// icon is name's emoji and a space, or nothing: a sound without an emoji keeps its text alone.
func (e *emojis) icon(name string) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if m, ok := e.m[name]; ok {
		return m + " "
	}
	return ""
}

// RunEmojis keeps the sound emojis current. A failed fetch keeps the last list: a stale emoji
// beats a sound losing its emoji over one bad request.
func (c *Commands) RunEmojis(ctx context.Context) error {
	t := time.NewTicker(emojisEvery)
	defer t.Stop()
	for {
		list, err := c.client.Rest.GetApplicationEmojis(c.client.ApplicationID, rest.WithCtx(ctx))
		if err != nil {
			slog.Warn("emojis: listing app emojis", slog.Any("err", err))
		} else {
			c.emojis.set(list)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}
