//go:build !linux

package hoststats

func readSnap() snap { return snap{} }
