//go:build !codeye_fixture

package main

// fixtureAgentEnv is empty in product builds: the agent gets only PATH and LANG.
var fixtureAgentEnv []string
