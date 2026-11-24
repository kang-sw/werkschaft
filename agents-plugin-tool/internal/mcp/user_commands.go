package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kang-sw/devenv/internal/wsusercmd"
)

// The user commands are bash lines an open hosted lead registers so the user
// can run them with a click in that lead's dashboard chat. The registry and
// the runner's authority live in the daemon (UserCommandHost); this file is the
// two tools, session.register_user_command and session.unregister_user_command.
// Their owner is always the connection's trusted conversation binding
// (Server.conversation), never a supplied session_key or reply ID.

const (
	registerUserCommandTool   = "session.register_user_command"
	unregisterUserCommandTool = "session.unregister_user_command"
)

func userCommandTool(name string) bool {
	return name == registerUserCommandTool || name == unregisterUserCommandTool
}

// UserCommandHost is the daemon's user commands as the tools see them, bound by
// the command layer (ServerOptions.UserCommands). Every method names the hosted
// conversation the connection's trusted binding resolved.
type UserCommandHost interface {
	// Lead refuses a conversation that is not an open hosted lead.
	Lead(conversation string) error
	// Register adds or replaces a command of the lead's bound worktree.
	Register(conversation string, in wsusercmd.Input) (wsusercmd.Command, wsusercmd.Outcome, error)
	// Unregister withdraws one; wsusercmd.ErrUnknown when there is none.
	Unregister(conversation, id string) error
}

// Distinct refusals of the user command tools, each raised before anything is
// written.
var (
	errUserCommandNotHosted   = errors.New("user commands belong to a session the dashboard hosts, and this connection is not one (its own session token names the session a command is registered for; a supplied session_key never does)")
	errUserCommandUnavailable = errors.New("user commands are unavailable: the daemon has not opened their store (it is still starting, or it could not open it)")
	errUserCommandLeadOnly    = errors.New("user commands are available to an open hosted lead session only")
)

// userCommandHost is the daemon's user commands, nil where there are none.
func (s *Server) userCommandHost() UserCommandHost {
	if s.userCommands == nil {
		return nil
	}
	return s.userCommands()
}

// userCommandsListed reports whether this connection is an authenticated open
// hosted lead (its trusted binding names the conversation and the runner says
// it is an open lead): only then are the tools advertised.
func (s *Server) userCommandsListed() bool {
	if s.conversation == "" {
		return false
	}
	// Discovery is the one lookup that may wait for a starting daemon; a
	// registration or withdrawal never waits and refuses until the store is open.
	lookup := s.userCommandsDiscovery
	if lookup == nil {
		lookup = s.userCommands
	}
	if lookup == nil {
		return false
	}
	host := lookup()
	return host != nil && host.Lead(s.conversation) == nil
}

func (s *Server) handleUserCommandTool(id json.RawMessage, name string, args map[string]any) response {
	if s.conversation == "" {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", name, errUserCommandNotHosted))
	}
	host := s.userCommandHost()
	if host == nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", name, errUserCommandUnavailable))
	}
	if err := host.Lead(s.conversation); err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w (%v)", name, errUserCommandLeadOnly, err))
	}
	text := func(key string) (string, error) {
		raw, present := args[key]
		if !present || raw == nil {
			return "", nil
		}
		v, ok := raw.(string)
		if !ok {
			return "", fmt.Errorf("%s: %s must be a string", name, key)
		}
		return v, nil
	}
	commandID, err := text("id")
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	commandID = strings.TrimSpace(commandID)
	if name == unregisterUserCommandTool {
		if err := host.Unregister(s.conversation, commandID); err != nil {
			return toolTextResponse(id, "", fmt.Errorf("%s: %w", name, err))
		}
		if wantsJSON(args) {
			return toolJSONResponse(id, map[string]any{"id": commandID, "status": "unregistered"}, nil)
		}
		return toolTextResponse(id, fmt.Sprintf("id: %s\nstatus: unregistered\n", commandID), nil)
	}
	in := wsusercmd.Input{ID: commandID}
	for key, dst := range map[string]*string{
		"label":       &in.Label,
		"description": &in.Description,
		"command":     &in.Line,
		"prompt":      &in.Prompt,
		"cwd":         &in.Cwd,
	} {
		if *dst, err = text(key); err != nil {
			return toolTextResponse(id, "", err)
		}
	}
	cmd, outcome, err := host.Register(s.conversation, in)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", name, err))
	}
	if wantsJSON(args) {
		return toolJSONResponse(id, map[string]any{"id": cmd.ID, "revision": cmd.Revision, "status": outcome.String()}, nil)
	}
	return toolTextResponse(id, fmt.Sprintf("id: %s\nrevision: %d\nstatus: %s\n", cmd.ID, cmd.Revision, outcome), nil)
}

func userCommandTools() []map[string]any {
	return []map[string]any{
		{"name": registerUserCommandTool, "description": fmt.Sprintf("Register a bash command line as a reusable command card in this session's dashboard chat, so the user can run it directly or ask the agent to run it with an optional prompt. Hosted lead sessions only. Upserts by id (a stable id you choose: letters, digits, '.', '_', '-', at most 64) and returns the stored revision; registering the same definition again changes nothing. Running executes the displayed line in your worktree root (or optional subdirectory cwd inside the worktree) as the user's own one-use approval; its result reaches your next turn as a receipt without starting or steering one, and a command still running after 10 s continues as an ordinary exec job. The line is shown to the user as written: keep it self-explanatory. Limits: %d commands per worktree, label at most %d characters, description at most %d characters, command and prompt at most 4 KiB each, cwd at most %d bytes inside the worktree root; the commands are kept for this session and worktree across restarts.", wsusercmd.MaxPerScope, wsusercmd.MaxLabel, wsusercmd.MaxDescription, wsusercmd.MaxCwd), "inputSchema": objectSchema(map[string]any{
			"session_key": stringProperty("Caller's lead session key."),
			"id":          stringProperty("Stable command id (required)."),
			"label":       stringProperty("Button label (required)."),
			"description": stringProperty("Optional one-line explanation for the command."),
			"command":     stringProperty("The bash line to run (required); a multiline script runs as one."),
			"prompt":      stringProperty("Optional default prompt instruction shown when asking the agent to run the command."),
			"cwd":         stringProperty("Optional working directory relative to (or inside) the session worktree root."),
			"format":      stringProperty("json for structured output; compact text otherwise."),
		}, "session_key", "id", "label", "command")},
		{"name": unregisterUserCommandTool, "description": "Withdraw a user command registered with session.register_user_command, by id. Hosted lead sessions only. The user's button disappears; a click already running is unaffected.", "inputSchema": objectSchema(map[string]any{
			"session_key": stringProperty("Caller's lead session key."),
			"id":          stringProperty("Command id to withdraw (required)."),
			"format":      stringProperty("json for structured output; compact text otherwise."),
		}, "session_key", "id")},
	}
}
