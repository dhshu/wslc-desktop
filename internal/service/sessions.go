package service

import (
	"context"
	"strconv"
	"strings"

	"github.com/wslc-desktop/wslc-desktop/internal/domain"
	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// ListSessions returns the current wslc sessions the client is attached to.
//
// This calls `wslc system session list` directly rather than reusing the
// session list embedded in EnvStatus.Sessions, so the UI can refresh it on
// demand (after a terminate, for example) without re-running the whole
// environment probe. The command rejects --format, so the table parser is
// the real path; see ENVIRONMENT.md.
func (s *Service) ListSessions(ctx context.Context) ([]domain.Session, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdSessionList, Args: []string{"system", "session", "list"}})
	if err != nil {
		return nil, err
	}
	list, err := wslc.ParseSessions(res.Stdout)
	if err != nil {
		return nil, err
	}
	return list, nil
}

// TerminateSession ends the wslc session with the given id.
//
// `wslc system session terminate` accepts no positional argument — the target
// is chosen with the global `--session <name>` flag. wslc's default session
// (the one created implicitly on first use) has name "default"; non-default
// sessions carry whatever name the user gave `wslc` at creation time. The
// command always takes effect on the session *identified by --session*,
// whether that session is the caller's current one or a different one.
//
// The id parameter is optional. When it is empty, the service forwards
// `--session default`, which is the only meaningful target the UI can offer
// without a running session list: terminating any other session would require
// the user to know its name in advance. Passing an id from the list is what
// the frontend does after the user picks a row.
//
// This is a destructive operation; the frontend must confirm twice before
// calling (see the AGENTS.md rule). The service does not confirm on its own
// because it cannot prompt.
func (s *Service) TerminateSession(ctx context.Context, sessionID int) (string, error) {
	if err := contextError(ctx); err != nil {
		return "", err
	}
	name, err := sessionNameForID(ctx, s, sessionID)
	if err != nil {
		return "", err
	}
	if name == "" {
		name = "default"
	}
	res, err := s.run(ctx, wslc.Spec{
		Kind: wslc.CmdSessionTerminate,
		Args: []string{"--session", name, "system", "session", "terminate"},
	})
	if err != nil {
		return "", err
	}
	return outputOr(res, "已终止会话 "+name), nil
}

// sessionNameForID looks up the display name for a session id by reading the
// session list once. An unknown id maps to the empty name, which the caller
// treats as "terminate default" so the call is still well-defined.
//
// The extra lookup exists because wslc's terminate command keys on the
// session *name*, not the id shown in the table. When the caller passes 0 we
// skip the lookup entirely: id 0 is not a real wslc session, so there is
// nothing to translate.
func sessionNameForID(ctx context.Context, s *Service, id int) (string, error) {
	if id <= 0 {
		return "", nil
	}
	sessions, err := s.ListSessions(ctx)
	if err != nil {
		// A failed list is not fatal: we still need to give the caller a
		// deterministic target, so fall back to the id rendered as a string
		// (wslc may accept either the numeric id or the name in this slot).
		return strconv.Itoa(id), nil
	}
	for _, sess := range sessions {
		if sess.ID == id {
			return strings.TrimSpace(sess.Name), nil
		}
	}
	// id not in the current list; use the numeric form as a best-effort name.
	return strconv.Itoa(id), nil
}
