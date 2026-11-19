//go:build !codeye_fixture

package main

// fixtureAgentEnv is empty in product builds: the agent gets only PATH and LANG.
var fixtureAgentEnv []string

// fixtureControllerEnv is empty in product builds.
var fixtureControllerEnv []string
