//go:build codeye_fixture

package main

// fixtureAgentEnv: test builds pass the Git mirror directory to the local
// agent (internal/agent's CODEYE_TEST_GIT_MIRROR), so `codeye up` with a
// GitHub address runs end to end without a network.
var fixtureAgentEnv = []string{"CODEYE_TEST_GIT_MIRROR"}
