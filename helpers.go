package pecs

import (
	"github.com/df-mc/dragonfly/server/cmd"
	"github.com/df-mc/dragonfly/server/player"
	"github.com/df-mc/dragonfly/server/player/form"
)

// Command extracts the player and session from a command source.
// Returns (nil, nil) if the source is not a player or has no session.
//
// Usage:
//
//	func (c MyCommand) Run(src cmd.Source, out *cmd.Output, tx *world.Tx) {
//	    p, sess := pecs.Command(src)
//	    if p == nil || sess == nil {
//	        out.Error("Player-only command")
//	        return
//	    }
//
//	    // Use p and sess...
//	}
//
// Concurrency:
// Commands are executed synchronously with the player, just like handlers.
// It is safe to access and modify components directly.
func Command(src cmd.Source) (*player.Player, *Session) {
	p, ok := src.(*player.Player)
	if !ok {
		return nil, nil
	}

	sess := GetSession(p)
	return p, sess
}

// Form extracts the player and session from a form submitter.
// Returns (nil, nil) if the submitter is not a player or has no session.
//
// Usage:
//
//	func (f MyForm) Submit(sub form.Submitter, tx *world.Tx) {
//	    p, sess := pecs.Form(sub)
//	    if p == nil || sess == nil {
//	        return
//	    }
//
//	    // Use p and sess...
//	}
//
// Concurrency:
// Form submissions are executed synchronously with the player, just like handlers.
// It is safe to access and modify components directly.
func Form(sub form.Submitter) (*player.Player, *Session) {
	p, ok := sub.(*player.Player)
	if !ok {
		return nil, nil
	}

	sess := GetSession(p)
	return p, sess
}

// MustCommand is like Command but panics if the source is not a valid player.
// Use this only when you're certain the source must be a player.
func MustCommand(src cmd.Source) (*player.Player, *Session) {
	p, sess := Command(src)
	if p == nil || sess == nil {
		panic("pecs: command source is not a player with a session")
	}
	return p, sess
}

// MustForm is like Form but panics if the submitter is not a valid player.
// Use this only when you're certain the submitter must be a player.
func MustForm(sub form.Submitter) (*player.Player, *Session) {
	p, sess := Form(sub)
	if p == nil || sess == nil {
		panic("pecs: form submitter is not a player with a session")
	}
	return p, sess
}
