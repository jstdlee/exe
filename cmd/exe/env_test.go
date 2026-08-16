package main

import "testing"

func TestCmdEnvUsageNoError(t *testing.T) {
	if err := cmdEnv(nil); err != nil {
		t.Fatal(err)
	}
	if err := cmdEnv([]string{"--help"}); err != nil {
		t.Fatal(err)
	}
}
