// Package ws provides a single-process, transport-only notification hub
// for "game state changed" events, plus the WebSocket plumbing that
// turns those events into a live connection. It knows nothing about
// game.Game or HTML — callers supply their own rendering.
package ws
