package pecs

import (
	"github.com/df-mc/dragonfly/server/cmd"
	"github.com/df-mc/dragonfly/server/item"
	"github.com/df-mc/dragonfly/server/player"
	"github.com/df-mc/dragonfly/server/player/form"
)

// getSessionFromPlayer extracts the session from a player's handler.
// Returns nil if the player doesn't have a PECS SessionHandler.
func getSessionFromPlayer(p *player.Player) *Session {
	h, ok := p.Handler().(*SessionHandler)
	if !ok {
		return nil
	}
	return h.session
}

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

	sess := getSessionFromPlayer(p)
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

	sess := getSessionFromPlayer(p)
	return p, sess
}

// Item extracts the player and session from an item user.
// Returns (nil, nil) if the user is not a player or has no session.
//
// Usage:
//
//	func (i MyItem) Use(tx *world.Tx, user item.User, ctx *item.UseContext) bool {
//	    p, sess := pecs.Item(user)
//	    if p == nil || sess == nil {
//	        return
//	    }
//
//	    // Use p and sess...
//	}
//
// Concurrency:
// Item uses are executed synchronously with the player, just like handlers.
// It is safe to access and modify components directly.
func Item(user item.User) (*player.Player, *Session) {
	p, ok := user.(*player.Player)
	if !ok {
		return nil, nil
	}

	sess := getSessionFromPlayer(p)
	return p, sess
}
