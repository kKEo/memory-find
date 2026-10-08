//go:build !unix

package discover

func trusted(string) error { return nil }
