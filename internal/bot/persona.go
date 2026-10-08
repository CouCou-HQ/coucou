package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/characters"
	"github.com/be-sandaa/coucou/internal/settings"
)

// Measured, not documented: Discord allows two avatar changes per guild in about ten minutes, and
// the route is per guild too. See docs/wiki/dev/Character-design.md.
const (
	personaPace       = time.Second
	avatarRetryAfter  = 10 * time.Minute
	avatarRateLimited = "AVATAR_RATE_LIMIT"
	// ponytail: polled, since Discord sends no event when another bot's avatar changes.
	appAvatarRefresh = time.Hour
	maxAvatarBytes   = 10 << 20
)

// Persona keeps every guild's nickname and avatar on its character, one guild per personaPace.
// A guild queued twice is pushed once, with whatever its character is by then.
type Persona struct {
	chars *characters.Set
	set   *settings.Store
	// self is the bot's nickname in a guild, false when the cache has no member for it.
	self   func(guild snowflake.ID) (nick string, ok bool)
	guilds func() []snowflake.ID
	update func(guild snowflake.ID, u discord.CurrentMemberUpdate) error
	after  func(d time.Duration, f func())
	// app is this bot's application. userAvatar is a bot's avatar hash, "" when it has none, and
	// download fetches it.
	app        snowflake.ID
	userAvatar func(ctx context.Context, user snowflake.ID) (string, error)
	download   func(ctx context.Context, user snowflake.ID, hash string) (*discord.Icon, error)
	apps       map[snowflake.ID]appAvatar // only touched from Run

	mu      sync.Mutex
	queue   []snowflake.ID
	pending map[snowflake.ID]bool
}

func NewPersona(c *bot.Client, chars *characters.Set, set *settings.Store) *Persona {
	return &Persona{
		chars: chars,
		set:   set,
		self: func(guild snowflake.ID) (string, bool) {
			m, ok := c.Caches.SelfMember(guild)
			if !ok || m.Nick == nil {
				return "", ok
			}
			return *m.Nick, true
		},
		guilds: func() []snowflake.ID {
			var ids []snowflake.ID
			for g := range c.Caches.Guilds() {
				ids = append(ids, g.ID)
			}
			return ids
		},
		update: func(guild snowflake.ID, u discord.CurrentMemberUpdate) error {
			_, err := c.Rest.UpdateCurrentMember(guild, u)
			return err
		},
		after: func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		app:   c.ApplicationID,
		userAvatar: func(ctx context.Context, user snowflake.ID) (string, error) {
			u, err := c.Rest.GetUser(user, rest.WithCtx(ctx))
			if err != nil || u.Avatar == nil {
				return "", err
			}
			return *u.Avatar, nil
		},
		download: downloadAvatar,
		apps:     map[snowflake.ID]appAvatar{},
		pending:  map[snowflake.ID]bool{},
	}
}

// appAvatar is a character's own bot's avatar, worn by a character without an avatar.* file.
type appAvatar struct {
	hash string // "app:" and Discord's hash, so it never matches an avatar.* hash; "" for none
	icon *discord.Icon
}

// downloadAvatar is the file as uploaded, a GIF for an animated avatar. No size: any size is a
// re-encode, and an upscaled GIF runs to megabytes where the original is a few hundred KB.
func downloadAvatar(ctx context.Context, user snowflake.ID, hash string) (*discord.Icon, error) {
	url := discord.User{ID: user, Avatar: &hash}.AvatarURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, *url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // read to the end already; a close error cannot change the icon
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("avatar of %s: %s", user, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAvatarBytes))
	if err != nil {
		return nil, err
	}
	return discord.ParseIconRaw(data)
}

// Push queues guilds to be brought in line with their characters.
func (p *Persona) Push(guilds ...snowflake.ID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, g := range guilds {
		if !p.pending[g] {
			p.pending[g] = true
			p.queue = append(p.queue, g)
		}
	}
}

// PushAll queues every guild the bot is in.
func (p *Persona) PushAll() { p.Push(p.guilds()...) }

func (p *Persona) next() (snowflake.ID, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.queue) == 0 {
		return 0, false
	}
	g := p.queue[0]
	p.queue = p.queue[1:]
	delete(p.pending, g)
	return g, true
}

func (p *Persona) Run(ctx context.Context) error {
	t := time.NewTicker(personaPace)
	defer t.Stop()
	refresh := time.NewTicker(appAvatarRefresh)
	defer refresh.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-refresh.C:
			p.refresh(ctx)
		case <-t.C:
			if g, ok := p.next(); ok {
				p.push(ctx, g)
			}
		}
	}
}

// avatar is what ch wears in a server: its avatar.* file, else its own bot's avatar, else none.
// This bot's own application is left out: wearing no guild avatar already shows its avatar.
func (p *Persona) avatar(ctx context.Context, ch *characters.Character) (*discord.Icon, string, error) {
	if ch.Avatar != nil || ch.App == 0 || ch.App == p.app {
		return ch.Avatar, ch.AvatarHash, nil
	}
	if a, ok := p.apps[ch.App]; ok {
		return a.icon, a.hash, nil
	}
	hash, err := p.userAvatar(ctx, ch.App)
	if err != nil {
		return nil, "", err
	}
	var a appAvatar
	if hash != "" {
		if a.icon, err = p.download(ctx, ch.App, hash); err != nil {
			return nil, "", err
		}
		a.hash = "app:" + hash
	}
	p.apps[ch.App] = a
	return a.icon, a.hash, nil
}

// refresh forgets every bot avatar that changed on Discord, and queues every guild to take it up.
func (p *Persona) refresh(ctx context.Context) {
	changed := false
	for app, a := range p.apps {
		hash, err := p.userAvatar(ctx, app)
		if err != nil {
			slog.Warn("persona: check avatar", slog.String("application_id", app.String()), slog.Any("err", err))
			continue
		}
		if hash == "" && a.hash != "" || hash != "" && a.hash != "app:"+hash {
			delete(p.apps, app)
			changed = true
		}
	}
	if changed {
		p.PushAll()
	}
}

// push sends the nickname and the avatar separately: Discord refuses a whole request when the
// avatar is rate limited, and the nickname should not wait ten minutes for it.
func (p *Persona) push(ctx context.Context, guild snowflake.ID) {
	st := p.set.Get(guild)
	ch := p.chars.Get(st.Character)
	log := slog.With(slog.String("guild", guild.String()), slog.String("character", ch.ID))

	if nick, ok := p.self(guild); ok && p.ours(nick) && nick != ch.Nickname {
		want := ch.Nickname // empty resets it to the bot's own name
		if err := p.update(guild, discord.CurrentMemberUpdate{Nick: &want}); err != nil {
			log.Log(ctx, level(err), "persona: nickname", slog.Any("err", err))
		}
	}

	icon, hash, err := p.avatar(ctx, ch)
	if err != nil {
		log.Warn("persona: avatar of its own bot", slog.String("application_id", ch.App.String()), slog.Any("err", err))
		return
	}
	if st.PushedAvatar == hash {
		return
	}
	// A nil icon is sent as null, which clears the guild avatar back to the bot's own.
	err = p.update(guild, discord.CurrentMemberUpdate{Avatar: omit.New(icon)})
	switch {
	case avatarLimited(err):
		log.Info("persona: avatar rate limited, retrying", slog.Duration("after", avatarRetryAfter))
		p.after(avatarRetryAfter, func() { p.Push(guild) })
		return
	case err != nil:
		log.Log(ctx, level(err), "persona: avatar", slog.Any("err", err))
		return
	}
	// Actor zero: the bot is keeping its own record of what it uploaded.
	if _, err := p.set.Update(ctx, guild, 0, func(s *settings.Settings) { s.PushedAvatar = hash }); err != nil {
		log.Warn("persona: record avatar", slog.Any("err", err))
	}
}

// ours is whether nick is one the bot set: none, or any character's. Anything else was chosen by
// the guild's admins and stays.
func (p *Persona) ours(nick string) bool {
	return nick == "" || slices.ContainsFunc(p.chars.All(), func(c *characters.Character) bool { return c.Nickname == nick })
}

// level is Debug for a guild that took Change Nickname away: its admins chose that, and every
// restart would otherwise warn about it again.
func level(err error) slog.Level {
	var re *rest.Error
	if errors.As(err, &re) && re.Code == rest.JSONErrorCodeLackPermissionsToPerformAction {
		return slog.LevelDebug
	}
	return slog.LevelWarn
}

type fieldError struct {
	Code string `json:"code"`
}

// avatarLimited is Discord refusing an avatar change as too soon. It is a 400 Invalid Form Body,
// not a 429, so disgo does not wait it out; the reason is only in the field errors.
func avatarLimited(err error) bool {
	var re *rest.Error
	if !errors.As(err, &re) || re.Code != rest.JSONErrorCodeInvalidFormBody {
		return false
	}
	var fields struct {
		Avatar struct {
			Errors []fieldError `json:"_errors"`
		} `json:"avatar"`
	}
	if json.Unmarshal(re.Errors, &fields) != nil {
		return false
	}
	return slices.ContainsFunc(fields.Avatar.Errors, func(e fieldError) bool { return e.Code == avatarRateLimited })
}
