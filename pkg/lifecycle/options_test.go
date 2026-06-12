// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package lifecycle

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOption_LeaderOnly(t *testing.T) {
	cfg := registrationConfig{}
	LeaderOnly()(&cfg)

	assert.True(t, cfg.leaderOnly, "LeaderOnly must set leaderOnly=true")

	// Re-applying must remain idempotent (no toggle).
	LeaderOnly()(&cfg)
	assert.True(t, cfg.leaderOnly, "applying LeaderOnly twice must remain true")
}
