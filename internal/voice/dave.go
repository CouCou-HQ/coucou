package voice

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/disgoorg/godave"
	"github.com/thomas-vilte/dave-go/session"
)

// sessions maps a voice conn to the DAVE session disgo created for it.
//
// disgo v0.19.6 builds the session inside voice.NewConn and exposes it nowhere on voice.Conn, so there
// is no way to ask a conn whether its MLS handshake has finished. The one hook it does give us is the
// session constructor — and godave hands that constructor the conn itself as the Callbacks argument,
// which makes the conn usable as the key. Entries are removed when Play closes the conn.
var sessions sync.Map // godave.Callbacks (the conn) → godave.Session

// SessionCreateFunc is what to hand voice.WithDaveSessionCreateFunc. It adapts dave-go's constructor,
// whose signature is (userID, callbacks, ...Option) rather than godave's (logger, userID, callbacks).
func SessionCreateFunc(logger *slog.Logger, userID godave.UserID, cb godave.Callbacks) godave.Session {
	s := session.New(userID, cb, session.WithLogger(logger))
	sessions.Store(cb, s)
	return s
}

// closeSession ends the DAVE session belonging to a conn that is being discarded, and forgets it.
//
// disgo v0.19.6 constructs the session and never closes it — godave only added Close to the
// interface in 0.3.0 — so the integrator has to, and the map kept for daveReady is what makes that
// possible. LoadAndDelete rather than Load and Delete: one map operation, and the entry goes on the
// same path whether or not the value turns out to be a session.
//
// Leaving it unclosed is not a permanent leak; dave-go's recovery watchdog re-arms at most three
// times at 15 s each and then exits on its own. But for those ~45 seconds a session belonging to a
// channel the bot has already left keeps re-arming MLS invalidations, which pollutes the log and
// can force a full voice reconnect.
func closeSession(conn any) {
	v, ok := sessions.LoadAndDelete(conn)
	if !ok {
		return // disgo's noop session, which was never registered
	}
	s, ok := v.(godave.Session)
	if !ok {
		return
	}
	// Documented as always nil and safe to call more than once. Checked anyway: it is an error
	// return, and a silently dropped one is how the next implementation's failure goes unnoticed.
	if err := s.Close(); err != nil {
		slog.Warn("closing the DAVE session", slog.Any("err", err))
	}
}

// daveReady reports whether frames can go out on the conn: its DAVE session has an active E2EE
// epoch, or the channel settled on protocol version 0 and never will. A conn with no registered
// session is one disgo built with the noop session, which never encrypts — nothing to wait for.
func daveReady(conn any) bool {
	v, ok := sessions.Load(conn)
	if !ok {
		return true
	}
	// Ready alone stays false forever on a v0 channel, which made every play there a timeout.
	if h, ok := v.(interface{ ShouldHoldFrames() bool }); ok {
		return !h.ShouldHoldFrames()
	}
	s, ok := v.(godave.Session)
	if !ok {
		return true
	}
	return s.Ready()
}

// daveState summarises where a stalled handshake stopped. welcomes=0 means no member ever
// committed the bot's add; joined>0 with no epoch means execute_transition never came.
func daveState(conn any) string {
	v, _ := sessions.Load(conn)
	s, ok := v.(*session.Session)
	if !ok {
		return "no dave-go session"
	}
	st, n := s.State(), s.Stats()
	return fmt.Sprintf("protocol=%d epoch=%d welcomes=%d/%d failed commits=%d/%d failed proposals_rejected=%d downgrades=%d",
		st.ProtocolVersion, st.EpochID, n.WelcomesJoined, n.WelcomesFailed,
		n.CommitsProcessed, n.CommitsFailed, n.ProposalsRejected, n.DowngradeToV0)
}
