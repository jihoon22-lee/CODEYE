//go:build codeye_fixture

package main

// fixtureAgentEnv: test builds pass the Git mirror directory to the local
// agent (internal/agent's CODEYE_TEST_GIT_MIRROR), so `codeye up` with a
// GitHub address runs end to end without a network.
var fixtureAgentEnv = []string{"CODEYE_TEST_GIT_MIRROR"}

// fixtureControllerEnv: test builds pass the fake GitHub API
// (internal/github's CODEYE_TEST_GITHUB_API) to the controller, which
// resolves pull requests and posts comments.
var fixtureControllerEnv = []string{"CODEYE_TEST_GITHUB_API"}
