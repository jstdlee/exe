//go:build !linux

package hoststats

func readSnap() snap { return snap{} }
func readProcs(int) []Proc { return nil }
