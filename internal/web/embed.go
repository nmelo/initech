// Package web provides an embedded HTTP server for the initech web companion.
// It serves a single-page application from embedded static files and exposes
// a JSON API for pane information.
package web

import "embed"

//go:embed static/*
var staticFS embed.FS
