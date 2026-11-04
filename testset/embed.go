// Package testsetdata embeds the test set so the binary can run it without the source tree.
package testsetdata

import "embed"

// Cases holds testset/cases/<gateway>/<id>.json.
//
//go:embed cases
var Cases embed.FS

// Results holds testset/results/*.json, the recorded runs against real gateways.
//
//go:embed results
var Results embed.FS
