//go:build !dev

package app

import "testing"

// A release build deploys globally and nothing else: DISCORD_DEV_GUILD is never read, and there is
// no dev_guild key to set.
func TestReleaseBuildIgnoresTheDevGuild(t *testing.T) {
	t.Setenv("DISCORD_DEV_GUILD", "123456789012345678")

	if _, err := load(t, minimal+"dev_guild = \"123456789012345678\""); err == nil {
		t.Error("parseConfig accepted dev_guild on a build that can only deploy globally")
	}

	// The environment variable alone must not stop a normal run either.
	if _, err := load(t, minimal); err != nil {
		t.Errorf("parseConfig: %v", err)
	}
}
