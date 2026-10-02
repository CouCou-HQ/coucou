//go:build !dev

package app

import "testing"

// A release build deploys globally and nothing else: DISCORD_DEV_GUILD is never read, and there is
// no -dev-guild flag to pass.
func TestReleaseBuildIgnoresTheDevGuild(t *testing.T) {
	clearEnv(t)
	t.Setenv("DISCORD_DEV_GUILD", "123456789012345678")

	if _, err := parseConfig([]string{flagDatabase, testDSN, flagToken, testToken, "-dev-guild", "123456789012345678"}); err == nil {
		t.Error("parseConfig accepted -dev-guild on a build that can only deploy globally")
	}

	// The environment variable alone must not stop a normal run either.
	if _, err := parseConfig([]string{flagDatabase, testDSN, flagToken, testToken}); err != nil {
		t.Errorf("parseConfig: %v", err)
	}
}
