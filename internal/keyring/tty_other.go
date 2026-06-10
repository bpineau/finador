//go:build !unix

package keyring

func ttyID() string { return "notty" }
